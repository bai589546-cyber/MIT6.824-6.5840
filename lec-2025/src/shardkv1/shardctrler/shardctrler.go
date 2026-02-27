package shardctrler

//
// Shardctrler with InitConfig, Query, and ChangeConfigTo methods
//

import (
	"fmt"
	"log"
	"strconv"

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
	// fmt.Printf("[InitController] START\n")
	// defer fmt.Printf("[InitController] END\n")

	// 检查是否有未完成的配置变更
	// 策略：如果存在 config-(latest+1)，说明有配置被保存但迁移未完成
	latestVer, _, err := sck.IKVClerk.Get("latest")
	if err != rpc.OK {
		// 无法获取 latest，返回
		// fmt.Printf("[InitController] Get latest failed, returning\n")
		return
	}
	// fmt.Printf("[InitController] latest=%s\n", latestVer)

	latestNum, _ := strconv.Atoi(latestVer)

	// 找到存在的最大编号的未完成配置（检查所有未来的 config）
	var nextNum shardcfg.Tnum
	var nextConfigValue string
	for i := 1; i <= 100; i++ { // 最多检查100个未来的配置
		candidateNum := shardcfg.Tnum(latestNum + int(i))
		candidateKey := "config-" + fmt.Sprint(candidateNum)
		value, _, err := sck.IKVClerk.Get(candidateKey)
		if err == rpc.OK {
			// 找到了，记录它，继续找更高的
			nextNum = candidateNum
			nextConfigValue = value
			// fmt.Printf("[InitController] config-%d exists\n", candidateNum)
		} else {
			// 没找到，停止
			break
		}
	}

	if nextNum == 0 {
		// 没有未完成的配置变更
		// fmt.Printf("[InitController] no incomplete configs found, nothing to recover\n")
		return
	}

	// fmt.Printf("[InitController] config-%d is the highest incomplete, starting recovery\n", nextNum)

	// 有未完成的配置变更，继续执行
	// // fmt.Println("[InitController] detected incomplete config", nextNum, ", continuing migration...")

	newCfg := shardcfg.FromString(nextConfigValue)
	oldCfg := sck.Query()

	if oldCfg == nil {
		// fmt.Printf("[InitController] oldCfg is nil, returning\n")
		return
	}

	// 获取 latest 的版本号用于后续的 Put 操作
	_, latestVersion, _ := sck.IKVClerk.Get("latest")
	// fmt.Printf("[InitController] starting migration, old.Num=%d new.Num=%d\n", oldCfg.Num, newCfg.Num)

	// 继续执行迁移（只需要迁移未完成的 shard）
	migrationCount := 0
	for shard := shardcfg.Tshid(0); shard < shardcfg.NShards; shard++ {
		oldGid := oldCfg.Shards[shard]
		newGid := newCfg.Shards[shard]

		// 如果 shard 没有变化，跳过
		if oldGid == newGid {
			continue
		}

		migrationCount++
		// fmt.Printf("[InitController] migrating shard %d from %d to %d\n", shard, oldGid, newGid)
		// Shard 需要迁移
		if oldGid != 0 && newGid != 0 {
			// 从 oldGid 迁移到 newGid
			sck.migrateShard(oldGid, newGid, shard, newCfg.Num, newCfg)
		} else if oldGid != 0 {
			// Shard 被移除
			sck.removeShard(oldGid, shard, newCfg.Num)
		}
	}

	// 迁移完成，更新 latest 指针
	// fmt.Printf("[InitController] migration done (%d shards), updating latest to %d\n", migrationCount, nextNum)
	sck.IKVClerk.Put("latest", fmt.Sprint(nextNum), latestVersion)
	// fmt.Printf("[InitController] config-%d recovery completed\n", nextNum)
	// // fmt.Println("[InitController] config", nextNum, "migration completed")
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

	// 2. 存储配置本身 (key = "config-N"，其中 N 是 cfg.Num)
	configKey := "config-" + fmt.Sprint(cfg.Num)
	sck.IKVClerk.Put(configKey, configStr, 0)

	// 3. 存储当前版本号 (key = "latest")
	sck.IKVClerk.Put("latest", fmt.Sprint(cfg.Num), 0)
}

// Called by the tester to ask the controller to change the
// configuration from the current one to new.  While the controller
// changes the configuration it may be superseded by another
// controller.
func (sck *ShardCtrler) ChangeConfigTo(new *shardcfg.ShardConfig) {
	// fmt.Printf("[ChangeConfigTo] START Num=%d\n", new.Num)
	// defer fmt.Printf("[ChangeConfigTo] END Num=%d\n", new.Num)

	// Your code here.
	// 1. 获取当前配置
	old := sck.Query()
	if old == nil {
		// fmt.Printf("[ChangeConfigTo] Query returned nil, returning\n")
		return
	}

	// fmt.Printf("[ChangeConfigTo] old.Num=%d new.Num=%d\n", old.Num, new.Num)

	// 2. 并发控制：检查是否已经被超越
	if old.Num >= new.Num {
		// 已经有更新的配置了，直接返回
		// fmt.Printf("[ChangeConfigTo] skipped: old.Num >= new.Num\n")
		return
	}

	// 3. 关键：先保存新配置（但不更新 latest），标记为"待处理"
	// 这样如果 controller 崩溃，新的 controller 可以检测到并继续
	configStr := new.String()
	configKey := "config-" + fmt.Sprint(new.Num)
	// fmt.Printf("[ChangeConfigTo] saving config-%d\n", new.Num)
	err := sck.IKVClerk.Put(configKey, configStr, 0)
	if err != rpc.OK {
		// fmt.Printf("[ChangeConfigTo] failed to save config-%d: %v, returning\n", new.Num, err)
		return
	}
	// fmt.Printf("[ChangeConfigTo] config-%d saved successfully\n", new.Num)

	// 4. 在迁移之前先获取 "latest" 的版本号（避免竞态条件）
	latestVer, latestVersion, _ := sck.IKVClerk.Get("latest")

	// 5. 再次检查：确保没有被其他 controller 超越
	// 重新查询 latest 配置，确认 new.Num 仍然是下一个配置
	latestVer2, _, _ := sck.IKVClerk.Get("latest")
	if latestVer2 != latestVer {
		// latest 在我们读取期间被修改了
		// fmt.Printf("[ChangeConfigTo] latest changed during processing, re-checking\n")
		latestNum, _ := strconv.Atoi(latestVer2)
		if shardcfg.Tnum(latestNum) >= new.Num {
			// fmt.Printf("[ChangeConfigTo] superseded (latest=%d >= new=%d), returning\n", latestNum, new.Num)
			return
		}
	}
	latestNum, _ := strconv.Atoi(latestVer2)
	if shardcfg.Tnum(latestNum) >= new.Num {
		// 已经有更新的配置了，直接返回（被其他 controller 超越了）
		// fmt.Printf("[ChangeConfigTo] superseded after saving config, returning\n")
		return
	}

	// 6. 遍历所有 shard，找出需要迁移的
	migrationCount := 0
	for shard := shardcfg.Tshid(0); shard < shardcfg.NShards; shard++ {
		oldGid := old.Shards[shard]
		newGid := new.Shards[shard]

		// 如果 shard 没有变化，跳过
		if oldGid == newGid {
			continue
		}

		migrationCount++
		// fmt.Printf("[ChangeConfigTo] migrating shard %d from %d to %d\n", shard, oldGid, newGid)
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

	// fmt.Printf("[ChangeConfigTo] migrations done: %d shards\n", migrationCount)

	// // fmt.Println("[ChangeConfigTo] migrations done:", migrationCount)

	// 7. 更新 latest 指针（使用之前获取的版本号），标记配置变更完成
	// 如果在此期间有其他 controller 完成了更新，这个 Put 会失败（CAS）
	sck.IKVClerk.Put("latest", fmt.Sprint(new.Num), latestVersion)

	// // fmt.Println("[ChangeConfigTo] config", new.Num, "saved")
}

func (sck *ShardCtrler) migrateShard(fromGid, toGid tester.Tgid, shard shardcfg.Tshid, num shardcfg.Tnum, new *shardcfg.ShardConfig) {
	// fmt.Printf("[migrateShard] START shard=%d fromGid=%d toGid=%d num=%d\n", shard, fromGid, toGid, num)
	// defer fmt.Printf("[migrateShard] END shard=%d\n", shard)

	// 在开始迁移前，检查当前配置，看这个 shard 是否还在 fromGid
	currentCfg := sck.Query()
	if currentCfg != nil {
		currentGid := currentCfg.Shards[shard]
		if currentGid != fromGid {
			// shard 已经迁移走了
			// fmt.Printf("[migrateShard] shard %d already moved from %d to %d, skipping\n", shard, fromGid, currentGid)
			return
		}
	}

	// 1. 从源 group 获取数据（从旧配置获取）
	servers := sck.getServersForGroup(fromGid)
	fromCk := shardgrp.MakeClerk(sck.clnt, servers)

	// 2. 冻结 shard
	// fmt.Printf("[migrateShard] calling FreezeShard...\n")
	state, err := fromCk.FreezeShard(shard, num)
	if err != rpc.OK {
		log.Fatalf("[migrateShard] FreezeShard failed: %v", err)
	}
	// fmt.Printf("[migrateShard] FreezeShard OK, state size=%d\n", len(state))

	// 3. 安装到目标 group（从新配置获取，因为 toGid 可能是新加入的 group）
	toServers := new.Groups[toGid]
	if toServers == nil {
		log.Fatalf("[migrateShard] InstallShard: toGid %d not found in new config Groups", toGid)
	}
	toCk := shardgrp.MakeClerk(sck.clnt, toServers)

	// fmt.Printf("[migrateShard] calling InstallShard...\n")
	err = toCk.InstallShard(shard, state, num)
	if err != rpc.OK {
		log.Fatalf("[migrateShard] InstallShard failed: %v", err)
	}
	// fmt.Printf("[migrateShard] InstallShard OK\n")

	// 4. 从源 group 删除
	// fmt.Printf("[migrateShard] calling DeleteShard...\n")
	err = fromCk.DeleteShard(shard, num)
	if err != rpc.OK {
		log.Fatalf("[migrateShard] DeleteShard failed: %v", err)
	}
	// fmt.Printf("[migrateShard] DeleteShard OK, DONE\n")
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
