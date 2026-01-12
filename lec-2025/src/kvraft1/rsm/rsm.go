package rsm

import (
	"sync"
	"time"
	// "fmt"
	"6.5840/kvsrv1/rpc"
	"6.5840/labrpc"
	"6.5840/raft1"
	"6.5840/raftapi"
	"6.5840/tester1"

)

var useRaftStateMachine bool // to plug in another raft besided raft1


type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	Me 		int
	Id 		int
	Req 	any
}


// A server (i.e., ../server.go) that wants to replicate itself calls
// MakeRSM and must implement the StateMachine interface.  This
// interface allows the rsm package to interact with the server for
// server-specific operations: the server must implement DoOp to
// execute an operation (e.g., a Get or Put request), and
// Snapshot/Restore to snapshot and restore the server's state.
type StateMachine interface {
	DoOp(any) any
	Snapshot() []byte
	Restore([]byte)
}



type RSM struct {
	mu           sync.Mutex
	me           int
	rf           raftapi.Raft
	applyCh      chan raftapi.ApplyMsg
	maxraftstate int // snapshot if log grows this big
	sm           StateMachine
	// Your definitions here.

	waitChs 	 map[int]chan Result
}

type Result struct {
	Index 		int
	Term 		int
	Value 		any
}

func (rsm *RSM) reader() {
	// 【关键修复】使用 range，当 applyCh 关闭时自动退出循环
    for msg := range rsm.applyCh {
        if msg.CommandValid {
            op, ok := msg.Command.(Op)
            if !ok { continue }
            
            // 1. 执行
            returnValue := rsm.sm.DoOp(op.Req)

            // 2. 尝试快照
            rsm.trySnapShot(msg.CommandIndex)

            // 3. 通知等待者
            rsm.mu.Lock()
            if waitCh, ok := rsm.waitChs[msg.CommandIndex]; ok {
                result := Result{
                    Index: msg.CommandIndex,
                    Term:  msg.CommandTerm,
                    Value: returnValue,
                }
                select {
                case waitCh <- result:
                default:
                }
            }
            rsm.mu.Unlock()

        } else if msg.SnapshotValid {
            // fmt.Println("rsm restore", rsm.me)
            rsm.sm.Restore(msg.Snapshot)
        }
    }

    // 循环退出说明 System Shutdown，清理善后
    rsm.mu.Lock()
    defer rsm.mu.Unlock()
    for _, ch := range rsm.waitChs {
        close(ch)
    }
}


// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant key/value service.
//
// me is the index of the current server in servers[].
//
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
// The RSM should snapshot when Raft's saved state exceeds maxraftstate bytes,
// in order to allow Raft to garbage-collect its log. if maxraftstate is -1,
// you don't need to snapshot.
//
// MakeRSM() must return quickly, so it should start goroutines for
// any long-running work.
func MakeRSM(servers []*labrpc.ClientEnd, me int, persister *tester.Persister, maxraftstate int, sm StateMachine) *RSM {
	rsm := &RSM{
		me:           me,
		maxraftstate: maxraftstate,
		applyCh:      make(chan raftapi.ApplyMsg),
		sm:           sm,
		waitChs:      make(map[int]chan Result), // <--- 【修复】必须初始化
	}

	// fmt.Println("rsm", rsm.me, "start...")
	if persister.SnapshotSize() > 0 {
        sm.Restore(persister.ReadSnapshot())
    }

	if !useRaftStateMachine {
		rsm.rf = raft.Make(servers, me, persister, rsm.applyCh)
	}

	go rsm.reader()
	return rsm
}

func (rsm *RSM) Raft() raftapi.Raft {
	return rsm.rf
}


func (rsm *RSM) trySnapShot(index int) {
	if (rsm.maxraftstate == -1) {
		return
	}
	// fmt.Println("rsm", rsm.me, "try snapshot, rsm.rf.PersistBytes() = ", rsm.rf.PersistBytes())
	if (rsm.maxraftstate < rsm.rf.PersistBytes()) {
		// persist
		// fmt.Println("rsm", rsm.me, "must snapshot, rsm.rf.PersistBytes() = ", rsm.rf.PersistBytes())
		buffer := rsm.sm.Snapshot()
		rsm.rf.Snapshot(index, buffer)
	}
	return
}

// Submit a command to Raft, and wait for it to be committed.  It
// should return ErrWrongLeader if client should find new leader and
// try again.
func (rsm *RSM) Submit(req any) (rpc.Err, any) {

	// Submit creates an Op structure to run a command through Raft;
	// for example: op := Op{Me: rsm.me, Id: id, Req: req}, where req
	// is the argument to Submit and id is a unique id for the op.

	// 1. call raft.Start()
	op := Op{
		Me:		rsm.me,
		Id:		0,
		Req:	req,
	}
	// fmt.Println("rsm", rsm.me, "submit", op)
	index, term, isLeader := rsm.rf.Start(op)

	if isLeader == false {
		// not the leader
		// fmt.Println("I am not the leader!")
		return rpc.ErrWrongLeader, nil // i'm dead, try another server.
	} 

	// 2. create a wait channel (one-shot channel)
	ch := make(chan Result, 1)

	// 3. register the wait channel to waitChs map
	rsm.mu.Lock()
	rsm.waitChs[index] = ch
	rsm.mu.Unlock()

	// 4. wait until receive msg from ch

	defer func() {
        rsm.mu.Lock()
        delete(rsm.waitChs, index)
        rsm.mu.Unlock()
    }()

	select {
	case result := <- ch:
		// 5. check whether the leader of raft has changed -> which means the index may be out of date
		// must check the "index == Result.Index"
		if result.Index == index && result.Term == term {
			return rpc.OK, result.Value
		}
		// fmt.Println("escape!")
		return rpc.ErrWrongLeader, nil
	case <-time.After(2000 * time.Millisecond):
        // 超时机制：防止 Raft 丢消息导致死等
		// fmt.Println("timeout")
        return rpc.ErrWrongLeader, nil
	}

}
