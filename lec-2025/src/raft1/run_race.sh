#!/bin/bash

# 定义日志文件名称
LOG_FILE="race.log"

# 清空之前的日志文件 (如果你希望保留之前的日志，可以注释掉这一行)
echo "" > "$LOG_FILE"

echo "开始运行测试，日志将写入 $LOG_FILE..."

# 循环运行 30 次
for i in {1..30}
do
    echo "正在运行第 $i 次测试..."
    
    # 在日志文件中写入分隔符，方便区分每一次运行
    echo "================= Iteration $i =================" >> "$LOG_FILE"
    
    # 运行测试并将 标准输出(stdout) 和 错误输出(stderr) 都追加到日志文件中
    # 2>&1 表示把错误输出也重定向到标准输出
    go test -run 3C -race >> "$LOG_FILE" 2>&1
    
    # 获取上一个命令（go test）的退出代码
    EXIT_CODE=$?
    
    # 检查测试是否失败
    if [ $EXIT_CODE -ne 0 ]; then
        echo "❌ 第 $i 次测试失败！"
        echo "日志详情请查看 $LOG_FILE"
        # 如果你希望一旦失败就停止脚本，取消下面这行的注释
        # exit 1
    else
        echo "✅ 第 $i 次测试通过"
    fi
done

echo "全部 30 次测试完成。"