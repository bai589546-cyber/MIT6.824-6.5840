package kvsrv

import (
	"log"
	"sync"
	"time"
)

const Debug = false

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		log.Printf(format, a...)
	}
	return
}

type ClientAckTime struct {
	AckID 		int
	UpdateTime 	time.Time
}

type KVServer struct {
	mu sync.Mutex

	// Your definitions here.
	KeyValue 	map[string]string
	AckTime 	[]ClientAckTime
	LenAckTime 	int
}


func (kv *KVServer) Get(args *GetArgs, reply *GetReply) {
	// Your code here.
	kv.mu.Lock()
	defer kv.mu.Unlock()

	value, ok := kv.KeyValue[args.Key]
	if ok {
		reply.Value = value
	} else {
		reply.Value = ""
	}
	

}

func (kv *KVServer) Put(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
	kv.mu.Lock()
	defer kv.mu.Unlock()

	value, ok := kv.KeyValue[args.Key]
	if ok {
		reply.Value = value
	} else {
		reply.Value = ""
	}
	kv.KeyValue[args.Key] = args.Value
}

func (kv *KVServer) Append(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
	kv.mu.Lock()
	defer kv.mu.Unlock()

	value, ok := kv.KeyValue[args.Key]
	if ok {
		reply.Value = value
	} else {
		reply.Value = ""
		value = ""
	}
	kv.KeyValue[args.Key] = value + args.Value
}

func StartKVServer() *KVServer {
	kv := new(KVServer)
	kv.KeyValue = make(map[string]string)
	// You may need initialization code here.
	kv.LenAckTime = 0
	return kv
}
