package kvsrv

import (
	"log"
	"sync"
	// "fmt"
)

const Debug = false

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		log.Printf(format, a...)
	}
	return
}

type ClientAckTime struct {
	AckID int

	ValueBuffer string
	// true = Put; false = Append
}

type KVServer struct {
	mu sync.Mutex

	// Your definitions here.
	KeyValue   map[string]string
	AckTime    []ClientAckTime
	LenAckTime int
}

func (kv *KVServer) NewClient(TransactionId int) int {
	NewClientId := kv.LenAckTime
	kv.LenAckTime = 1 + kv.LenAckTime
	client := ClientAckTime{TransactionId, ""}
	kv.AckTime = append(kv.AckTime, client)
	return NewClientId
}

func (kv *KVServer) Get(args *GetArgs, reply *GetReply) {
	// Your code here.
	kv.mu.Lock()
	defer kv.mu.Unlock()
	reply.ClientId = args.ClientId
	value, ok := kv.KeyValue[args.Key]

	if ok {
		reply.Value = value
	} else {
		reply.Value = ""
	}
	// fmt.Println("Get, value = ", reply.Value)

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

	var clientId int
	clientId = args.ClientId
	if clientId == -1 {
		clientId = kv.NewClient(args.TransactionId)
		reply.ClientId = clientId
		reply.Value = ""
		reply.AckId = args.TransactionId
		return
	}
	if args.TransactionId == (kv.AckTime[clientId].AckID) {

		kv.AckTime[clientId].AckID = 1 + kv.AckTime[clientId].AckID

		// kv.AckTime[clientId].ValueBuffer = kv.KeyValue[args.Key]
		// no need to store backup for PUT operation???????
		kv.KeyValue[args.Key] = args.Value
		// actually do put
	} else {
		reply.Value = kv.AckTime[clientId].ValueBuffer
		// actually do put
	}
	reply.ClientId = clientId
	reply.AckId = kv.AckTime[clientId].AckID

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

	var clientId int
	clientId = args.ClientId
	if clientId == -1 {
		clientId = kv.NewClient(args.TransactionId)
		reply.ClientId = clientId
		reply.Value = ""
		reply.AckId = args.TransactionId
		return
		// fmt.Println("Append, id = ", kv.AckTime[clientId].AckID)
	}
	if args.TransactionId == (kv.AckTime[clientId].AckID) {
		// fmt.Println("Append, id++ = ", kv.AckTime[clientId].AckID)
		kv.AckTime[clientId].AckID = 1 + kv.AckTime[clientId].AckID

		kv.AckTime[clientId].ValueBuffer = kv.KeyValue[args.Key]

		kv.KeyValue[args.Key] = kv.KeyValue[args.Key] + args.Value
		// actually do append
	} else {
		reply.Value = kv.AckTime[clientId].ValueBuffer

	}
	reply.ClientId = clientId
	reply.AckId = kv.AckTime[clientId].AckID

}

func StartKVServer() *KVServer {
	kv := new(KVServer)
	kv.KeyValue = make(map[string]string)
	kv.LenAckTime = 0
	return kv
}
