package shardctrler

//
// Shardctrler with InitConfig, Query, and ChangeConfigTo methods
//

import (
	"fmt"
	"log"

	kvraft "6.5840/kvraft1"
	"6.5840/kvsrv1/rpc"
	kvtest "6.5840/kvtest1"
	"6.5840/shardkv1/shardcfg"
	"6.5840/shardkv1/shardgrp"
	tester "6.5840/tester1"
)

// ShardCtrler for the controller and kv clerk.
type ShardCtrler struct {
	clnt *tester.Clnt
	kvtest.IKVClerk

	killed int32 // set by Kill()

	// Your data here.
}

// Make a ShardCltler, which stores its state in a kvraft.
func MakeShardCtrler(clnt *tester.Clnt) *ShardCtrler {
	sck := &ShardCtrler{clnt: clnt}
	// 生成所有 GRP0 组的服务器名称（3个服务器）
	servers := make([]string, 3)
	for i := 0; i < 3; i++ {
		servers[i] = tester.ServerName(tester.GRP0, i)
	}
	sck.IKVClerk = kvraft.MakeClerk(clnt, servers)
	return sck
}

// The tester calls InitController() before starting a new
// controller. In part A, this method doesn't need to do anything. In
// B and C, this method implements recovery.
func (sck *ShardCtrler) InitController() {
}

// Called once by the tester to supply the first configuration.  You
// can marshal ShardConfig into a string using shardcfg.String(), and
// then Put it in the kvsrv for the controller at version 0.  You can
// pick the key to name the configuration.  The initial configuration
// lists shardgrp shardcfg.Gid1 for all shards.
func (sck *ShardCtrler) InitConfig(cfg *shardcfg.ShardConfig) {
	// Your code here
	// 1. 序列化配置
	configStr := cfg.String()

	// 2. 存储配置本身 (key = "config-0")
	sck.IKVClerk.Put("config-0", configStr, 0)

	// 3. 存储当前版本号 (key = "latest")
	sck.IKVClerk.Put("latest", "0", 0)
}

// Called by the tester to ask the controller to change the
// configuration from the current one to new.  While the controller
// changes the configuration it may be superseded by another
// controller.
func (sck *ShardCtrler) ChangeConfigTo(new *shardcfg.ShardConfig) {
	// Your code here.
	// 1. 获取当前配置
	old := sck.Query()
	if old == nil {
		// // fmt.Println("[ChangeConfigTo] Query returned nil")
		return
	}

	// // fmt.Println("[ChangeConfigTo] old.Num=", old.Num, "new.Num=", new.Num, "new.Groups=", len(new.Groups))

	// 2. 并发控制：检查是否已经被超越
	if old.Num >= new.Num {
		// 已经有更新的配置了，直接返回
		// fmt.Println("[ChangeConfigTo] skipped: already superseded")
		return
	}

	// 3. 在迁移之前先获取 "latest" 的版本号（避免竞态条件）
	_, latestVersion, _ := sck.IKVClerk.Get("latest")

	// 4. 遍历所有 shard，找出需要迁移的
	migrationCount := 0
	for shard := shardcfg.Tshid(0); shard < shardcfg.NShards; shard++ {
		oldGid := old.Shards[shard]
		newGid := new.Shards[shard]

		// 如果 shard 没有变化，跳过
		if oldGid == newGid {
			continue
		}

		migrationCount++
		// Shard 需要迁移
		if oldGid != 0 && newGid != 0 {
			// 从 oldGid 迁移到 newGid
			sck.migrateShard(oldGid, newGid, shard, new.Num, new)
		} else if oldGid != 0 {
			// Shard 被移除 (没有新的 group 接收)
			sck.removeShard(oldGid, shard, new.Num)
		} else if newGid != 0 {
			// 新 shard 加入（没有旧数据）
			// 不需要迁移，只需要创建空的 shard
		}
	}

	// // fmt.Println("[ChangeConfigTo] migrations done:", migrationCount)

	// 5. 先保存新配置内容
	configStr := new.String()
	configKey := "config-" + fmt.Sprint(new.Num)
	sck.IKVClerk.Put(configKey, configStr, 0) // 新 key，version 用 0

	// 6. 更新 latest 指针（使用之前获取的版本号）
	sck.IKVClerk.Put("latest", fmt.Sprint(new.Num), latestVersion)

	// // fmt.Println("[ChangeConfigTo] config", new.Num, "saved")
}

func (sck *ShardCtrler) migrateShard(fromGid, toGid tester.Tgid, shard shardcfg.Tshid, num shardcfg.Tnum, new *shardcfg.ShardConfig) {
	// 在开始迁移前，检查当前配置，看这个 shard 是否还在 fromGid
	currentCfg := sck.Query()
	if currentCfg != nil {
		currentGid := currentCfg.Shards[shard]
		if currentGid != fromGid {
			// fmt.Println("[migrateShard] shard", shard, "already moved from", fromGid, "to", currentGid, ", skipping")
			return
		}
	}

	// 1. 从源 group 获取数据（从旧配置获取）
	servers := sck.getServersForGroup(fromGid)
	fromCk := shardgrp.MakeClerk(sck.clnt, servers)

	// 2. 冻结 shard
	// fmt.Println("[migrateShard] calling FreezeShard shard=", shard, "num=", num, "fromGid=", fromGid, "toGid=", toGid)
	state, err := fromCk.FreezeShard(shard, num)
	if err != rpc.OK {
		log.Fatalf("FreezeShard failed: %v", err)
	}
	// fmt.Println("[migrateShard] FreezeShard OK, state size:", len(state))

	// 3. 安装到目标 group（从新配置获取，因为 toGid 可能是新加入的 group）
	toServers := new.Groups[toGid]
	if toServers == nil {
		log.Fatalf("InstallShard: toGid %d not found in new config Groups", toGid)
	}
	toCk := shardgrp.MakeClerk(sck.clnt, toServers)

	// fmt.Println("[migrateShard] calling InstallShard...")
	err = toCk.InstallShard(shard, state, num)
	if err != rpc.OK {
		log.Fatalf("InstallShard failed: %v", err)
	}
	// fmt.Println("[migrateShard] InstallShard OK")

	// 4. 从源 group 删除
	// fmt.Println("[migrateShard] calling DeleteShard...")
	err = fromCk.DeleteShard(shard, num)
	if err != rpc.OK {
		log.Fatalf("DeleteShard failed: %v", err)
	}
	// fmt.Println("[migrateShard] DeleteShard OK, DONE")
}

func (sck *ShardCtrler) removeShard(gid tester.Tgid, shard shardcfg.Tshid, num shardcfg.Tnum) {
	servers := sck.getServersForGroup(gid)
	ck := shardgrp.MakeClerk(sck.clnt, servers)

	err := ck.DeleteShard(shard, num)
	if err != rpc.OK {
		log.Fatalf("DeleteShard failed: %v", err)
	}
}

func (sck *ShardCtrler) getServersForGroup(gid tester.Tgid) []string {
	// 从当前配置中获取 group 的服务器列表
	cfg := sck.Query()
	if servers, ok := cfg.Groups[gid]; ok {
		return servers
	}
	return nil
}

// Return the current configuration
func (sck *ShardCtrler) Query() *shardcfg.ShardConfig {
	// 1. 获取最新版本号
	latestVer, _, err := sck.IKVClerk.Get("latest")
	if err != rpc.OK {
		// fmt.Println("[Query] Get latest failed:", err)
		return nil
	}

	// 2. 获取配置内容
	configKey := "config-" + latestVer
	configValue, _, err := sck.IKVClerk.Get(configKey)
	if err != rpc.OK {
		// fmt.Println("[Query] Get config", configKey, "failed:", err)
		return nil
	}

	// 3. 反序列化
	cfg := shardcfg.FromString(configValue)
	// // fmt.Println("[Query] success: Num=", cfg.Num, "Groups=", len(cfg.Groups))
	return cfg
}
