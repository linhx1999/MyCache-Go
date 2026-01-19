# MyCache-Go

## 安装

```bash
rm -rf /usr/local/go && tar -C /usr/local -xzf go1.22.12.linux-amd64.tar.gz

vim ~/.bashrc

export PATH=$PATH:/usr/local/go/bin
source $HOME/.profile
source $HOME/.bashrc

# 验证安装
go version
```

### 安装不同版本

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

### 安装ETCD

```bash
docker run -d --name etcd \
-p 2379:2379 \
quay.io/coreos/etcd:v3.5.0 \
etcd --advertise-client-urls http://0.0.0.0:2379 \
--listen-client-urls http://0.0.0.0:2379    
```

## 运行

### 启动容器

```bash
docker compose up -d
```