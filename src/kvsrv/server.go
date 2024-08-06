package kvsrv

import (
	"log"
	"sync"
	"time"
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
	AckID 		int64
	UpdateTime 	time.Time
}

type KVServer struct {
	mu sync.Mutex

	// Your definitions here.
	KeyValue 	map[string]string
	AckTime 	[]ClientAckTime
	LenAckTime 	int

}

func (kv *KVServer) NewClient(TransactionId int64) int {
	NewClientId := kv.LenAckTime
	kv.LenAckTime = 1 + kv.LenAckTime
	client := ClientAckTime{TransactionId, time.Now()}
	kv.AckTime = append(kv.AckTime, client)
	return NewClientId
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

	// fmt.Println("Get, origin value = ", value)
	// fmt.Println("Get, transaction id = ", args.TransactionId)
	// fmt.Println("Get, client id = ", args.ClientId)


	var clientId int = args.ClientId
	if clientId != -1 && args.TransactionId == (kv.AckTime[clientId].AckID) {
		kv.AckTime[clientId].AckID = 1 + kv.AckTime[clientId].AckID
		kv.AckTime[clientId].UpdateTime = time.Now()
	}
	reply.ClientId = args.ClientId
	reply.AckId = args.TransactionId + 1

	if clientId != -1 {
		reply.AckId = kv.AckTime[clientId].AckID
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

	// fmt.Println("Put, origin value = ", value)
	// fmt.Println("Put, value to append = ", args.Value)
	// fmt.Println("Put, transaction id = ", args.TransactionId)
	// fmt.Println("Put, client id = ", args.ClientId)

	var clientId int
	clientId = args.ClientId
	if clientId == -1 {
		clientId = kv.NewClient(args.TransactionId)
	}
	if args.TransactionId == (kv.AckTime[clientId].AckID) {
		kv.AckTime[clientId].AckID = 1 + kv.AckTime[clientId].AckID
		kv.AckTime[clientId].UpdateTime = time.Now()

		kv.KeyValue[args.Key] = args.Value
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
	// fmt.Println("Append, origin value = ", value)
	// fmt.Println("Append, value to append = ", args.Value)
	// fmt.Println("Append, transaction id = ", args.TransactionId)
	// fmt.Println("Append, client id = ", args.ClientId)

	var clientId int
	clientId = args.ClientId
	if clientId == -1 {
		clientId = kv.NewClient(args.TransactionId)
		// fmt.Println("Append, id = ", kv.AckTime[clientId].AckID)
	}
	if args.TransactionId == (kv.AckTime[clientId].AckID) {
		// fmt.Println("Append, id++ = ", kv.AckTime[clientId].AckID)
		kv.AckTime[clientId].AckID = 1 + kv.AckTime[clientId].AckID
		kv.AckTime[clientId].UpdateTime = time.Now()

		kv.KeyValue[args.Key] = value + args.Value
		// actually do append
	}
	reply.ClientId = clientId
	reply.AckId = kv.AckTime[clientId].AckID
	
}

func StartKVServer() *KVServer {
	kv := new(KVServer)
	kv.KeyValue = make(map[string]string)
	// You may need initialization code here.
	kv.LenAckTime = 0
	return kv
}
