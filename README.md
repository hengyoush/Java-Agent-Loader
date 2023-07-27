# Java-Agent-Loader
不需要JVM就可以attach指定java agent到指定JVM中

# 使用方法
./agent_loader {pid} /path/to/your-agent.jar

# 实现原理
JVM利用了Unix Domain Socket跨进程通信的机制，JVM指定了一个文件创建socket，由用户写入命令到socket，JVM从其中读取命令执行并返回响应。
其详细步骤如下所述：
 1. java_pid文件 
 2.创建attach_pid文件 
 3.发送QUIT信号，等待JVM创建java_pid文件 
 4. connect socket 
 5. write命令 protocol version + cmd + args 
 6. read response

