package shardkv

//
// client code to talk to a sharded key/value service.
//
// the client uses the shardctrler to query for the current
// configuration and find the assignment of shards (keys) to groups,
// and then talks to the group that holds the key's shard.
//

import (
	"6.5840/kvsrv1/rpc"
	kvtest "6.5840/kvtest1"
	"6.5840/shardkv1/shardcfg"
	"6.5840/shardkv1/shardctrler"
	"6.5840/shardkv1/shardgrp"
	tester "6.5840/tester1"
)

type Clerk struct {
	clnt *tester.Clnt
	sck  *shardctrler.ShardCtrler
	// You will have to modify this struct.
}

// The tester calls MakeClerk and passes in a shardctrler so that
// client can call it's Query method
func MakeClerk(clnt *tester.Clnt, sck *shardctrler.ShardCtrler) kvtest.IKVClerk {
	ck := &Clerk{
		clnt: clnt,
		sck:  sck,
	}
	// You'll have to add code here.
	return ck
}

// Get a key from a shardgrp.  You can use shardcfg.Key2Shard(key) to
// find the shard responsible for the key and ck.sck.Query() to read
// the current configuration and lookup the servers in the group
// responsible for key.  You can make a clerk for that group by
// calling shardgrp.MakeClerk(ck.clnt, servers).
func (ck *Clerk) Get(key string) (string, rpc.Tversion, rpc.Err) {
	// 重试循环：如果收到 ErrWrongGroup，重新查询配置并重试
	for {
		// 1. 获取配置
		cfg := ck.sck.Query()

		// 2. 计算 key 属于哪个 shard
		shard := shardcfg.Key2Shard(key)

		// 3. 找到负责该 shard 的 group ID
		gid := cfg.Shards[shard]

		// 4. 获取该 group 的服务器列表
		servers := cfg.Groups[gid]

		// 5. 创建 ShardGrp clerk 并调用 Get
		grpCk := shardgrp.MakeClerk(ck.clnt, servers)
		value, version, err := grpCk.Get(key)

		// 6. 如果收到 ErrWrongGroup，重新查询配置并重试
		if err == rpc.ErrWrongGroup {
			continue
		}
		return value, version, err
	}
}

// Put a key to a shard group.
func (ck *Clerk) Put(key string, value string, version rpc.Tversion) rpc.Err {
	// 重试循环：如果收到 ErrWrongGroup，重新查询配置并重试
	for {
		// 1. 获取配置
		cfg := ck.sck.Query()

		// 2. 计算 key 属于哪个 shard
		shard := shardcfg.Key2Shard(key)

		// 3. 找到负责该 shard 的 group ID
		gid := cfg.Shards[shard]

		// 4. 获取该 group 的服务器列表
		servers := cfg.Groups[gid]

		// 5. 创建 ShardGrp clerk 并调用 Put
		grpCk := shardgrp.MakeClerk(ck.clnt, servers)
		err := grpCk.Put(key, value, version)

		// 6. 如果收到 ErrWrongGroup，重新查询配置并重试
		if err == rpc.ErrWrongGroup {
			continue
		}
		return err
	}
}
