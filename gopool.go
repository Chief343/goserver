package goserver

import (
	"context"
	"sync"
	"time"
)

//gPool .
//针对key值进行并行调用的协程池,同一个key下的任务串行,不同key下的任务并行
type gPool struct {
	ctx     context.Context
	taskNum int
	exp     time.Duration
	m       sync.Map
	sign    chan struct{}
}

func newgPoll(ctx context.Context, perItemTaskNum int, exp time.Duration, parallelSize int) *gPool {
	g := &gPool{
		ctx:     ctx,
		taskNum: perItemTaskNum,
		exp:     exp,
		sign:    make(chan struct{}, parallelSize),
	}
	return g
}

//SchduleByKey 为不同key值下的任务并行调用,相同key值下的任务串行调用,并行任务量和串行任务量由配置参数决定
func (g *gPool) SchduleByKey(key interface{}, task func()) bool {
	if v, ok := g.m.Load(key); ok { //希望在同一协程下顺序执行
		gItem := v.(*gItem)
		return gItem.DoOrInChan(task)
	}
	select {
	case <-g.ctx.Done():
		return false
	case g.sign <- struct{}{}:
		gItem := newgItem(g.ctx, g.taskNum, g.exp, func() {
			g.m.Delete(key)
			select {
			case <-g.sign:
			default:
			}
		})
		g.m.Store(key, gItem)
		return gItem.DoOrInChan(task)
	}
}

type gItem struct {
	tasks  chan func()     //任务通道
	sign   chan struct{}   //是否加入任务通道信号
	ctx    context.Context //退出协程信号
	exp    time.Duration
	onExit func()
}

func newgItem(ctx context.Context, taskNum int, exp time.Duration, onExit func()) *gItem {
	return &gItem{
		tasks:  make(chan func(), taskNum),
		sign:   make(chan struct{}, 1), //
		ctx:    ctx,
		exp:    exp,
		onExit: onExit,
	}
}

func (g *gItem) DoOrInChan(task func()) bool {
	select {
	case <-g.ctx.Done():
		return false
	case g.tasks <- task:
		return true
	case g.sign <- struct{}{}:
		go g.worker()
		return g.DoOrInChan(task)
	default:
		return false
	}
}

func (g *gItem) worker() {
	timer := time.NewTimer(g.exp)
	defer timer.Stop()
	defer func() {
		select {
		case <-g.sign:
		default:
		}
		if g.onExit != nil {
			g.onExit()
		}
	}()
	for {
		select {
		case <-g.ctx.Done():
			return
		case task := <-g.tasks: //执行任务优先
			//timer.Reset(g.exp)
			/*
				1) 如果重置时间,那么会在任务全部处理完成后继续等待过期,虽然空闲等待是一种资源浪费,但这主要用于复用当前协程对任务队列的执行
				2) 如果不重置时间,那么当前协程会在有效期内执行任务队列,但超过时间后协程只会创建给下一个任务队列
				3) 个人认为,不重置时间可均衡各个任务队列之间的任务调度
			*/
			task()
		case <-timer.C:
			return
		}
	}
}
