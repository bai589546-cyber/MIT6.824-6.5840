package kvraft

import (
	"sync/atomic"
	"sync"
	"log"
	// "fmt"
	"6.5840/kvraft1/rsm"
	"6.5840/kvsrv1/rpc"
	"6.5840/labgob"
	"6.5840/labrpc"
	"6.5840/tester1"

	"bytes"

)

type KVServer struct {
	me   int
	dead int32 // set by Kill()
	rsm  *rsm.RSM

	// Your definitions here.
	mu           	sync.Mutex
	KeyValue 		map[string]ValueEntry
}

type ValueEntry struct {
	Value 		string
	Version 	rpc.Tversion
}

// To type-cast req to the right type, take a look at Go's type switches or type
// assertions below:
//
// https://go.dev/tour/methods/16
// https://go.dev/tour/methods/15
func (kv *KVServer) DoOp(req any) any {
	// Your code here

	switch req.(type) {
	case *rpc.GetArgs:
		getreq := req.(*rpc.GetArgs)
		getReply := rpc.GetReply{}
		kv.mu.Lock()
		entry, ok := kv.KeyValue[getreq.Key]
		kv.mu.Unlock()
		if !ok {
			getReply.Err = rpc.ErrNoKey
		} else if ok {
			getReply.Value = entry.Value
			getReply.Version = entry.Version
			getReply.Err = rpc.OK
		}
		return getReply
	case *rpc.PutArgs:
		putreq := req.(*rpc.PutArgs)
		putReply := rpc.PutReply{}
		kv.mu.Lock()
		entry, ok := kv.KeyValue[putreq.Key]
		
		if !ok {
			if putreq.Version == 0 {
				// put 对应的 key 为空，需要 version == 0 才能创建
				kv.KeyValue[putreq.Key] = ValueEntry{putreq.Value, 1}
				putReply.Err = rpc.OK
			} else {
				putReply.Err = rpc.ErrVersion
			}
			
		} else if ok {
			if putreq.Version != entry.Version {
				putReply.Err = rpc.ErrVersion
			} else {
				kv.KeyValue[putreq.Key] = ValueEntry{putreq.Value, entry.Version + 1}
				putReply.Err = rpc.OK
			}
		}
		kv.mu.Unlock()
		return putReply
	case rpc.GetArgs:
		getreq := req.(rpc.GetArgs)
		getReply := rpc.GetReply{}
		kv.mu.Lock()
		entry, ok := kv.KeyValue[getreq.Key]
		kv.mu.Unlock()
		if !ok {
			getReply.Err = rpc.ErrNoKey
		} else if ok {
			getReply.Value = entry.Value
			getReply.Version = entry.Version
			getReply.Err = rpc.OK
		}
		return getReply
	case rpc.PutArgs:
		putreq := req.(rpc.PutArgs)
		putReply := rpc.PutReply{}
		kv.mu.Lock()
		entry, ok := kv.KeyValue[putreq.Key]
		
		if !ok {
			if putreq.Version == 0 {
				// put 对应的 key 为空，需要 version == 0 才能创建
				kv.KeyValue[putreq.Key] = ValueEntry{putreq.Value, 1}
				putReply.Err = rpc.OK
			} else {
				putReply.Err = rpc.ErrVersion
			}
			
		} else if ok {
			if putreq.Version != entry.Version {
				putReply.Err = rpc.ErrVersion
			} else {
				kv.KeyValue[putreq.Key] = ValueEntry{putreq.Value, entry.Version + 1}
				putReply.Err = rpc.OK
			}
		}
		kv.mu.Unlock()
		return putReply
	default:
		// wrong type! expecting an Inc.
		log.Fatalf("DoOp cannot handle the REQUEST %T", req)
	}
	return nil
}

func (kv *KVServer) Snapshot() []byte {
	// Your code here
	kv.mu.Lock() // 加锁
    defer kv.mu.Unlock()

	writeBuffer := new(bytes.Buffer)
	encoder := labgob.NewEncoder(writeBuffer)

	// 编码错误检查是好习惯，虽然 labgob panic 也会被此时捕获
    if err := encoder.Encode(kv.KeyValue); err != nil {
        log.Fatalf("Snapshot encode error: %v", err)
    }
	kvSnapshot := writeBuffer.Bytes()
	// fmt.Println("kvserver", kv.me, "snapshot size = ", len(kvSnapshot))
	return kvSnapshot
}

func (kv *KVServer) Restore(data []byte) {
	// Your code here
	if data == nil || len(data) < 1 {
		return
	}

	// 1. create io.reader buffer
	r := bytes.NewBuffer(data)
	// 2. create decoder
	d := labgob.NewDecoder(r)
	// 3. prepare variable to receive data
    var kvBuffer 	map[string]ValueEntry

	kv.mu.Lock()
	// 4. decode
	// decode order must match with encode order
	if d.Decode(&kvBuffer) != nil {		// 新增
	  log.Printf("Error: KV server %d failed to RESTORE\n", kv.me)
	} else {
		kv.KeyValue = kvBuffer
	}

	kv.mu.Unlock()
}

func (kv *KVServer) Get(args *rpc.GetArgs, reply *rpc.GetReply) {
	// Your code here. Use kv.rsm.Submit() to submit args
	// You can use go's type casts to turn the any return value
	// of Submit() into a GetReply: rep.(rpc.GetReply)

	// fmt.Println("me = ", kv.me, "Get Request, key =", args.Key)

	err, getValue := kv.rsm.Submit(args)
	reply.Err = err

	getResult, ok := getValue.(rpc.GetReply)
	if err == rpc.OK && ok{
		reply.Value = getResult.Value
		reply.Version = getResult.Version
		reply.Err = getResult.Err
	}

}

func (kv *KVServer) Put(args *rpc.PutArgs, reply *rpc.PutReply) {
	// Your code here. Use kv.rsm.Submit() to submit args
	// You can use go's type casts to turn the any return value
	// of Submit() into a PutReply: rep.(rpc.PutReply)

	// fmt.Println("me = ", kv.me, "Put Request, key =", args.Key, "value =", args.Value)

	err, putValue := kv.rsm.Submit(args)
	reply.Err = err

	putResult, ok := putValue.(rpc.PutReply)
	if err == rpc.OK && ok{
		reply.Err = putResult.Err
	}
}

// the tester calls Kill() when a KVServer instance won't
// be needed again. for your convenience, we supply
// code to set rf.dead (without needing a lock),
// and a killed() method to test rf.dead in
// long-running loops. you can also add your own
// code to Kill(). you're not required to do anything
// about this, but it may be convenient (for example)
// to suppress debug output from a Kill()ed instance.
func (kv *KVServer) Kill() {
	atomic.StoreInt32(&kv.dead, 1)
	// Your code here, if desired.
}

func (kv *KVServer) killed() bool {
	z := atomic.LoadInt32(&kv.dead)
	return z == 1
}

// StartKVServer() and MakeRSM() must return quickly, so they should
// start goroutines for any long-running work.
func StartKVServer(servers []*labrpc.ClientEnd, gid tester.Tgid, me int, persister *tester.Persister, maxraftstate int) []tester.IService {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(rsm.Op{})
	labgob.Register(rpc.PutArgs{})
	labgob.Register(rpc.GetArgs{})

	kv := &KVServer{me: me}


	kv.rsm = rsm.MakeRSM(servers, me, persister, maxraftstate, kv)
	// You may need initialization code here.
	kv.dead = 0
	if kv.KeyValue == nil {
        kv.KeyValue = make(map[string]ValueEntry)
    }


	return []tester.IService{kv, kv.rsm.Raft()}
}
