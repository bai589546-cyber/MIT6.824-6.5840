package shardgrp

import (
	"time"

	"6.5840/kvsrv1/rpc"
	"6.5840/shardkv1/shardcfg"
	"6.5840/shardkv1/shardgrp/shardrpc"
	tester "6.5840/tester1"
)

type Clerk struct {
	clnt    *tester.Clnt
	servers []string
	// You will have to modify this struct.
	lastLeader int
}

func MakeClerk(clnt *tester.Clnt, servers []string) *Clerk {
	ck := &Clerk{clnt: clnt, servers: servers}
	ck.lastLeader = -1
	return ck
}

func (ck *Clerk) Get(key string) (string, rpc.Tversion, rpc.Err) {
	// You will have to modify this function.
	args := rpc.GetArgs{
		key,
	}

	i := ck.lastLeader
	if i == -1 {
		i = 0
	}
	for {
		reply := rpc.GetReply{}
		ok := ck.clnt.Call(ck.servers[i], "KVServer.Get", &args, &reply)
		if ok && reply.Err != rpc.ErrWrongLeader {
			ck.lastLeader = i
			return reply.Value, reply.Version, reply.Err
		}
		i = (i + 1) % len(ck.servers)
		if i == 0 || !ok {
			time.Sleep(50 * time.Millisecond) // 不是 Leader 就睡一会
		}
	}
	// return reply.Value, reply.Version, reply.Err
}

func (ck *Clerk) Put(key string, value string, version rpc.Tversion) rpc.Err {
	// You will have to modify this function.
	args := rpc.PutArgs{
		key,
		value,
		version,
	}

	i := ck.lastLeader
	if i == -1 {
		i = 0
	}

	isResend := false
	retryCount := 0
	for {
		reply := rpc.PutReply{}
		ok := ck.clnt.Call(ck.servers[i], "KVServer.Put", &args, &reply)
		if ok && reply.Err != rpc.ErrWrongLeader {
			// fmt.Println("Put", args, "send to kvsrv", i)

			ck.lastLeader = i
			if isResend && reply.Err == rpc.ErrVersion {
				reply.Err = rpc.ErrMaybe
			}
			return reply.Err
		}
		i = (i + 1) % len(ck.servers)
		isResend = true
		retryCount++
		if retryCount%100 == 0 {
			// fmt.Printf("[ShardGrp Clerk] Put(%s) retrying, count=%d, servers=%v\n", key, retryCount, ck.servers)
		}
		if i == 0 || !ok {
			time.Sleep(50 * time.Millisecond) // 不是 Leader 就睡一会
		}
	}
	// return reply.Err
}

func (ck *Clerk) FreezeShard(s shardcfg.Tshid, num shardcfg.Tnum) ([]byte, rpc.Err) {
	// Your code here
	args := shardrpc.FreezeShardArgs{
		Shard: s,
		Num:   num,
	}

	i := ck.lastLeader
	if i == -1 {
		i = 0
	}

	for {
		reply := shardrpc.FreezeShardReply{}
		ok := ck.clnt.Call(ck.servers[i], "KVServer.FreezeShard", &args, &reply)

		if ok && reply.Err == rpc.OK {
			ck.lastLeader = i
			return reply.State, reply.Err
		}

		// 处理错误
		if reply.Err == rpc.ErrMaybe {
			// 可能是重复请求或网络问题，重试
		}

		i = (i + 1) % len(ck.servers)
		if i == 0 || !ok {
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func (ck *Clerk) InstallShard(s shardcfg.Tshid, state []byte, num shardcfg.Tnum) rpc.Err {
	// Your code here
	args := shardrpc.InstallShardArgs{
		Shard: s,
		State: state,
		Num:   num,
	}

	i := ck.lastLeader
	if i == -1 {
		i = 0
	}

	for {
		reply := shardrpc.InstallShardReply{}
		ok := ck.clnt.Call(ck.servers[i], "KVServer.InstallShard", &args, &reply)

		if ok && reply.Err == rpc.OK {
			ck.lastLeader = i
			return reply.Err
		}

		i = (i + 1) % len(ck.servers)
		if i == 0 || !ok {
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func (ck *Clerk) DeleteShard(s shardcfg.Tshid, num shardcfg.Tnum) rpc.Err {
	// Your code here
	args := shardrpc.DeleteShardArgs{
		Shard: s,
		Num:   num,
	}

	i := ck.lastLeader
	if i == -1 {
		i = 0
	}

	for {
		reply := shardrpc.DeleteShardReply{}
		ok := ck.clnt.Call(ck.servers[i], "KVServer.DeleteShard", &args, &reply)

		if ok && reply.Err == rpc.OK {
			ck.lastLeader = i
			return reply.Err
		}

		i = (i + 1) % len(ck.servers)
		if i == 0 || !ok {
			time.Sleep(50 * time.Millisecond)
		}
	}
}
