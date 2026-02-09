# MyCache-Go

## 安装

### 安装Go

```bash
rm -rf /usr/local/go && tar -C /usr/local -xzf go1.22.12.linux-amd64.tar.gz

vim ~/.bashrc

export PATH=$PATH:/usr/local/go/bin
source $HOME/.profile
source $HOME/.bashrc

# 验证安装
go version
```

### 安装不同版本的Go

```bash
# 安装不同版本
go install golang.org/dl/go1.21.13@latest
cd ~/go/bin
./go1.21.13 download

vim ~/.bashrc
# 修改 PATH 环境变量
export PATH=$PATH:$HOME/sdk/go1.21.13/bin
source $HOME/.profile
source $HOME/.bashrc
```

## 运行

### 启动ETCD

```bash
docker compose up -d
```

### 运行示例

```bash
go run example/test.go -port 8001 -node A
go run example/test.go -port 8002 -node B
go run example/test.go -port 8003 -node C
```



## 格式化代码

```bash
gofmt -w .
```