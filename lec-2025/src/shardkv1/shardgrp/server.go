package shardgrp

import (
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"

	"6.5840/kvraft1/rsm"
	"6.5840/kvsrv1/rpc"
	"6.5840/labgob"
	"6.5840/labrpc"
	"6.5840/shardkv1/shardcfg"
	"6.5840/shardkv1/shardgrp/shardrpc"
	tester "6.5840/tester1"
)

type KVServer struct {
	me   int
	dead int32 // set by Kill()
	rsm  *rsm.RSM
	gid  tester.Tgid

	// Your code here
	mu       sync.Mutex
	KeyValue map[string]ValueEntry // key value pair

	// 新增：shard 迁移相关
	frozenShards   map[shardcfg.Tshid]bool          // 哪些 shard 被冻结
	maxShardNum    map[shardcfg.Tshid]shardcfg.Tnum // 每个 shard 的最大 Num
	migratedShards map[shardcfg.Tshid]bool          // 哪些 shard 已被迁移走
}

type ValueEntry struct {
	Value   string
	Version rpc.Tversion
}

func (kv *KVServer) DoOp(req any) any {
	// Your code here
	// Your code here

	switch req.(type) {
	case *shardrpc.FreezeShardArgs:
		return kv.doFreezeShard(req.(*shardrpc.FreezeShardArgs))
	case shardrpc.FreezeShardArgs:
		args := req.(shardrpc.FreezeShardArgs)
		return kv.doFreezeShard(&args)

	case *shardrpc.InstallShardArgs:
		return kv.doInstallShard(req.(*shardrpc.InstallShardArgs))
	case shardrpc.InstallShardArgs:
		args := req.(shardrpc.InstallShardArgs)
		return kv.doInstallShard(&args)

	case *shardrpc.DeleteShardArgs:
		return kv.doDeleteShard(req.(*shardrpc.DeleteShardArgs))
	case shardrpc.DeleteShardArgs:
		args := req.(shardrpc.DeleteShardArgs)
		return kv.doDeleteShard(&args)

	case *rpc.GetArgs:
		getreq := req.(*rpc.GetArgs)
		getReply := rpc.GetReply{}
		shard := shardcfg.Key2Shard(getreq.Key)
		kv.mu.Lock()
		// 检查 shard 是否已被迁移走
		if migrated, ok := kv.migratedShards[shard]; ok && migrated {
			kv.mu.Unlock()
			// fmt.Printf("[DoOp Get] gid=%d key=%s shard=%d ErrWrongGroup (migrated)\n", kv.gid, getreq.Key, shard)
			getReply.Err = rpc.ErrWrongGroup
			return getReply
		}
		// 检查 shard 是否被冻结，如果是则返回 ErrWrongGroup 让客户端重试
		if frozen, ok := kv.frozenShards[shard]; ok && frozen {
			kv.mu.Unlock()
			// fmt.Printf("[DoOp Get] gid=%d key=%s shard=%d ErrWrongGroup (frozen)\n", kv.gid, getreq.Key, shard)
			getReply.Err = rpc.ErrWrongGroup
			return getReply
		}
		entry, ok := kv.KeyValue[getreq.Key]
		kv.mu.Unlock()
		if !ok {
			// fmt.Printf("[DoOp Get] gid=%d key=%s shard=%d ErrNoKey\n", kv.gid, getreq.Key, shard)
			getReply.Err = rpc.ErrNoKey
		} else {
			// fmt.Printf("[DoOp Get] gid=%d key=%s shard=%d OK version=%d\n", kv.gid, getreq.Key, shard, entry.Version)
			getReply.Value = entry.Value
			getReply.Version = entry.Version
			getReply.Err = rpc.OK
		}
		return getReply
	case *rpc.PutArgs:
		putreq := req.(*rpc.PutArgs)
		putReply := rpc.PutReply{}
		shard := shardcfg.Key2Shard(putreq.Key)
		kv.mu.Lock()
		// 检查 shard 是否已被迁移走
		if migrated, ok := kv.migratedShards[shard]; ok && migrated {
			kv.mu.Unlock()
			// fmt.Printf("[DoOp Put] gid=%d key=%s shard=%d version=%d ErrWrongGroup (migrated)\n", kv.gid, putreq.Key, shard, putreq.Version)
			putReply.Err = rpc.ErrWrongGroup
			return putReply
		}
		// 检查 shard 是否被冻结，如果是则返回 ErrWrongGroup 让客户端重试
		if frozen, ok := kv.frozenShards[shard]; ok && frozen {
			kv.mu.Unlock()
			// fmt.Printf("[DoOp Put] gid=%d key=%s shard=%d version=%d ErrWrongGroup (frozen)\n", kv.gid, putreq.Key, shard, putreq.Version)
			putReply.Err = rpc.ErrWrongGroup
			return putReply
		}
		entry, ok := kv.KeyValue[putreq.Key]

		if !ok {
			if putreq.Version == 0 {
				// put 对应的 key 为空，需要 version == 0 才能创建
				kv.KeyValue[putreq.Key] = ValueEntry{putreq.Value, 1}
				// fmt.Printf("[DoOp Put] gid=%d key=%s shard=%d version=%d OK (created)\n", kv.gid, putreq.Key, shard, putreq.Version)
				putReply.Err = rpc.OK
			} else {
				// fmt.Printf("[DoOp Put] gid=%d key=%s shard=%d version=%d ErrVersion (key not exist)\n", kv.gid, putreq.Key, shard, putreq.Version)
				putReply.Err = rpc.ErrVersion
			}

		} else {
			if putreq.Version != entry.Version {
				// fmt.Printf("[DoOp Put] gid=%d key=%s shard=%d req_version=%d current_version=%d ErrVersion\n", kv.gid, putreq.Key, shard, putreq.Version, entry.Version)
				putReply.Err = rpc.ErrVersion
			} else {
				kv.KeyValue[putreq.Key] = ValueEntry{putreq.Value, entry.Version + 1}
				// fmt.Printf("[DoOp Put] gid=%d key=%s shard=%d version=%d OK (updated to %d)\n", kv.gid, putreq.Key, shard, putreq.Version, entry.Version+1)
				putReply.Err = rpc.OK
			}
		}
		kv.mu.Unlock()
		return putReply
	case rpc.GetArgs:
		getreq := req.(rpc.GetArgs)
		getReply := rpc.GetReply{}
		shard := shardcfg.Key2Shard(getreq.Key)
		kv.mu.Lock()
		// 检查 shard 是否已被迁移走
		if migrated, ok := kv.migratedShards[shard]; ok && migrated {
			kv.mu.Unlock()
			// fmt.Printf("[DoOp Get] gid=%d key=%s shard=%d ErrWrongGroup (migrated)\n", kv.gid, getreq.Key, shard)
			getReply.Err = rpc.ErrWrongGroup
			return getReply
		}
		// 检查 shard 是否被冻结，如果是则返回 ErrWrongGroup 让客户端重试
		if frozen, ok := kv.frozenShards[shard]; ok && frozen {
			kv.mu.Unlock()
			// fmt.Printf("[DoOp Get] gid=%d key=%s shard=%d ErrWrongGroup (frozen)\n", kv.gid, getreq.Key, shard)
			getReply.Err = rpc.ErrWrongGroup
			return getReply
		}
		entry, ok := kv.KeyValue[getreq.Key]
		kv.mu.Unlock()
		if !ok {
			// fmt.Printf("[DoOp Get] gid=%d key=%s shard=%d ErrNoKey\n", kv.gid, getreq.Key, shard)
			getReply.Err = rpc.ErrNoKey
		} else {
			// fmt.Printf("[DoOp Get] gid=%d key=%s shard=%d OK version=%d\n", kv.gid, getreq.Key, shard, entry.Version)
			getReply.Value = entry.Value
			getReply.Version = entry.Version
			getReply.Err = rpc.OK
		}
		return getReply
	case rpc.PutArgs:
		putreq := req.(rpc.PutArgs)
		putReply := rpc.PutReply{}
		shard := shardcfg.Key2Shard(putreq.Key)
		kv.mu.Lock()
		// 检查 shard 是否已被迁移走
		if migrated, ok := kv.migratedShards[shard]; ok && migrated {
			kv.mu.Unlock()
			// fmt.Printf("[DoOp Put] gid=%d key=%s shard=%d version=%d ErrWrongGroup (migrated)\n", kv.gid, putreq.Key, shard, putreq.Version)
			putReply.Err = rpc.ErrWrongGroup
			return putReply
		}
		// 检查 shard 是否被冻结，如果是则返回 ErrWrongGroup 让客户端重试
		if frozen, ok := kv.frozenShards[shard]; ok && frozen {
			kv.mu.Unlock()
			// fmt.Printf("[DoOp Put] gid=%d key=%s shard=%d version=%d ErrWrongGroup (frozen)\n", kv.gid, putreq.Key, shard, putreq.Version)
			putReply.Err = rpc.ErrWrongGroup
			return putReply
		}
		entry, ok := kv.KeyValue[putreq.Key]

		if !ok {
			if putreq.Version == 0 {
				// put 对应的 key 为空，需要 version == 0 才能创建
				kv.KeyValue[putreq.Key] = ValueEntry{putreq.Value, 1}
				// fmt.Printf("[DoOp Put] gid=%d key=%s shard=%d version=%d OK (created)\n", kv.gid, putreq.Key, shard, putreq.Version)
				putReply.Err = rpc.OK
			} else {
				// fmt.Printf("[DoOp Put] gid=%d key=%s shard=%d version=%d ErrVersion (key not exist)\n", kv.gid, putreq.Key, shard, putreq.Version)
				putReply.Err = rpc.ErrVersion
			}

		} else {
			if putreq.Version != entry.Version {
				// fmt.Printf("[DoOp Put] gid=%d key=%s shard=%d req_version=%d current_version=%d ErrVersion\n", kv.gid, putreq.Key, shard, putreq.Version, entry.Version)
				putReply.Err = rpc.ErrVersion
			} else {
				kv.KeyValue[putreq.Key] = ValueEntry{putreq.Value, entry.Version + 1}
				// fmt.Printf("[DoOp Put] gid=%d key=%s shard=%d version=%d OK (updated to %d)\n", kv.gid, putreq.Key, shard, putreq.Version, entry.Version+1)
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
	return nil
}

func (kv *KVServer) Restore(data []byte) {
	// Your code here
}

func (kv *KVServer) Get(args *rpc.GetArgs, reply *rpc.GetReply) {
	err, getValue := kv.rsm.Submit(args)
	reply.Err = err

	getResult, ok := getValue.(rpc.GetReply)
	if err == rpc.OK && ok {
		reply.Value = getResult.Value
		reply.Version = getResult.Version
		reply.Err = getResult.Err
	}
}

func (kv *KVServer) Put(args *rpc.PutArgs, reply *rpc.PutReply) {
	// Your code here
	err, putValue := kv.rsm.Submit(args)
	reply.Err = err

	putResult, ok := putValue.(rpc.PutReply)
	if err == rpc.OK && ok {
		reply.Err = putResult.Err
	}
}

func (kv *KVServer) doFreezeShard(args *shardrpc.FreezeShardArgs) shardrpc.FreezeShardReply {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	reply := shardrpc.FreezeShardReply{}

	// fmt.Printf("[doFreezeShard] gid=%d shard=%d num=%d called\n", kv.gid, args.Shard, args.Num)

	// 检查 Num
	if maxNum, ok := kv.maxShardNum[args.Shard]; ok {
		if args.Num < maxNum {
			// 旧请求，拒绝
			// fmt.Printf("[doFreezeShard] gid=%d shard=%d num=%d OLD (maxNum=%d) -> ErrMaybe\n", kv.gid, args.Shard, args.Num, maxNum)
			reply.Err = rpc.ErrMaybe
			return reply
		} else if args.Num == maxNum {
			// 重复请求（幂等性）
			// 如果 shard 已经被迁移走（在 migratedShards 中），返回空数据
			if migrated, ok := kv.migratedShards[args.Shard]; ok && migrated {
				// fmt.Printf("[doFreezeShard] gid=%d shard=%d num=%d DUPLICATE (maxNum=%d, shard migrated) -> OK (empty)\n", kv.gid, args.Shard, args.Num, maxNum)
				reply.State = []byte("{}")
				reply.Num = args.Num
				reply.Err = rpc.OK
				return reply
			}
			// 否则重新收集数据返回
			// fmt.Printf("[doFreezeShard] gid=%d shard=%d num=%d DUPLICATE (maxNum=%d, returning collected data)\n", kv.gid, args.Shard, args.Num, maxNum)
			shardData := make(map[string]ValueEntry)
			for key, entry := range kv.KeyValue {
				if shardcfg.Key2Shard(key) == args.Shard {
					shardData[key] = entry
				}
			}
			state, err := json.Marshal(shardData)
			if err != nil {
				reply.Err = rpc.ErrMaybe
				return reply
			}
			reply.State = state
			reply.Num = args.Num
			reply.Err = rpc.OK
			return reply
		}
	}

	// 标记 shard 为冻结
	kv.frozenShards[args.Shard] = true
	kv.maxShardNum[args.Shard] = args.Num

	// 收集该 shard 的所有数据
	shardData := make(map[string]ValueEntry)
	for key, entry := range kv.KeyValue {
		if shardcfg.Key2Shard(key) == args.Shard {
			shardData[key] = entry
		}
	}

	// 序列化数据（使用 json 或 labgob）
	state, err := json.Marshal(shardData)
	if err != nil {
		reply.Err = rpc.ErrMaybe
		return reply
	}

	// fmt.Printf("[doFreezeShard] gid=%d shard=%d num=%d OK, state size=%d\n", kv.gid, args.Shard, args.Num, len(state))
	reply.State = state
	reply.Num = args.Num
	reply.Err = rpc.OK
	return reply
}

func (kv *KVServer) doInstallShard(args *shardrpc.InstallShardArgs) shardrpc.InstallShardReply {
	reply := shardrpc.InstallShardReply{}

	// fmt.Printf("[doInstallShard] gid=%d shard=%d num=%d called\n", kv.gid, args.Shard, args.Num)

	kv.mu.Lock()
	defer kv.mu.Unlock()

	// 检查 Num
	if maxNum, ok := kv.maxShardNum[args.Shard]; ok {
		if args.Num < maxNum {
			// 旧请求，拒绝
			// fmt.Printf("[doInstallShard] gid=%d shard=%d num=%d OLD (maxNum=%d) -> ErrMaybe\n", kv.gid, args.Shard, args.Num, maxNum)
			reply.Err = rpc.ErrMaybe
			return reply
		} else if args.Num == maxNum {
			// 重复请求（幂等性），直接返回 OK
			// fmt.Printf("[doInstallShard] gid=%d shard=%d num=%d DUPLICATE -> OK\n", kv.gid, args.Shard, args.Num)
			reply.Err = rpc.OK
			return reply
		}
	}

	kv.maxShardNum[args.Shard] = args.Num

	// 清除 migratedShards 标记（shard 被安装到当前 group，就不再被认为是"已迁移"）
	delete(kv.migratedShards, args.Shard)

	// 解析并安装数据
	var shardData map[string]ValueEntry
	if err := json.Unmarshal(args.State, &shardData); err != nil {
		// fmt.Printf("[doInstallShard] gid=%d shard=%d num=%d Unmarshal FAILED -> ErrMaybe\n", kv.gid, args.Shard, args.Num)
		reply.Err = rpc.ErrMaybe
		return reply
	}

	// 安装数据
	for key, entry := range shardData {
		kv.KeyValue[key] = entry
	}

	// 解冻 shard（安装完成后，shard 就属于当前 group 了，不应该被冻结）
	delete(kv.frozenShards, args.Shard)

	// fmt.Printf("[doInstallShard] gid=%d shard=%d num=%d OK, installed %d keys\n", kv.gid, args.Shard, args.Num, len(shardData))
	reply.Err = rpc.OK
	return reply
}

func (kv *KVServer) doDeleteShard(args *shardrpc.DeleteShardArgs) shardrpc.DeleteShardReply {
	reply := shardrpc.DeleteShardReply{}

	// fmt.Printf("[doDeleteShard] gid=%d shard=%d num=%d called\n", kv.gid, args.Shard, args.Num)

	kv.mu.Lock()
	defer kv.mu.Unlock()

	// 检查 Num
	if maxNum, ok := kv.maxShardNum[args.Shard]; ok {
		if args.Num < maxNum {
			// 旧请求，拒绝
			// fmt.Printf("[doDeleteShard] gid=%d shard=%d num=%d OLD (maxNum=%d) -> ErrMaybe\n", kv.gid, args.Shard, args.Num, maxNum)
			reply.Err = rpc.ErrMaybe
			return reply
		} else if args.Num == maxNum {
			// 重复请求，确保标记已迁移并解冻
			kv.migratedShards[args.Shard] = true
			delete(kv.frozenShards, args.Shard)
			// fmt.Printf("[doDeleteShard] gid=%d shard=%d num=%d DUPLICATE (already deleted) -> OK\n", kv.gid, args.Shard, args.Num)
			reply.Err = rpc.OK
			return reply
		}
	}

	kv.maxShardNum[args.Shard] = args.Num

	// 标记 shard 已被迁移走
	kv.migratedShards[args.Shard] = true

	// 删除该 shard 的所有数据
	deletedCount := 0
	for key := range kv.KeyValue {
		if shardcfg.Key2Shard(key) == args.Shard {
			delete(kv.KeyValue, key)
			deletedCount++
		}
	}

	// 解冻 shard
	delete(kv.frozenShards, args.Shard)

	// fmt.Printf("[doDeleteShard] gid=%d shard=%d num=%d OK, deleted %d keys\n", kv.gid, args.Shard, args.Num, deletedCount)
	reply.Err = rpc.OK
	return reply
}

// Freeze the specified shard (i.e., reject future Get/Puts for this
// shard) and return the key/values stored in that shard.
func (kv *KVServer) FreezeShard(args *shardrpc.FreezeShardArgs, reply *shardrpc.FreezeShardReply) {
	// Your code here
	err, value := kv.rsm.Submit(args)
	reply.Err = err

	result, ok := value.(shardrpc.FreezeShardReply)
	if err == rpc.OK && ok {
		*reply = result // 注意：复制整个结构体
	}
}

// Install the supplied state for the specified shard.
func (kv *KVServer) InstallShard(args *shardrpc.InstallShardArgs, reply *shardrpc.InstallShardReply) {
	// Your code here
	err, _ := kv.rsm.Submit(args)
	reply.Err = err
}

// Delete the specified shard.
func (kv *KVServer) DeleteShard(args *shardrpc.DeleteShardArgs, reply *shardrpc.DeleteShardReply) {
	// Your code here
	err, _ := kv.rsm.Submit(args)
	reply.Err = err
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

// StartShardServerGrp starts a server for shardgrp `gid`.
//
// StartShardServerGrp() and MakeRSM() must return quickly, so they should
// start goroutines for any long-running work.
func StartServerShardGrp(servers []*labrpc.ClientEnd, gid tester.Tgid, me int, persister *tester.Persister, maxraftstate int) []tester.IService {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(rpc.PutArgs{})
	labgob.Register(rpc.GetArgs{})
	labgob.Register(shardrpc.FreezeShardArgs{})
	labgob.Register(shardrpc.InstallShardArgs{})
	labgob.Register(shardrpc.DeleteShardArgs{})
	labgob.Register(rsm.Op{})

	labgob.Register(map[string]ValueEntry{})
	labgob.Register(ValueEntry{})
	labgob.Register(shardcfg.Tshid(0))
	labgob.Register(shardcfg.Tnum(0))

	kv := &KVServer{gid: gid, me: me}
	kv.rsm = rsm.MakeRSM(servers, me, persister, maxraftstate, kv)

	// Your code here

	// 初始化 KeyValue
	kv.dead = 0
	if kv.KeyValue == nil {
		kv.KeyValue = make(map[string]ValueEntry)
		kv.frozenShards = make(map[shardcfg.Tshid]bool)
		kv.maxShardNum = make(map[shardcfg.Tshid]shardcfg.Tnum)
		kv.migratedShards = make(map[shardcfg.Tshid]bool)
	}

	return []tester.IService{kv, kv.rsm.Raft()}
}
