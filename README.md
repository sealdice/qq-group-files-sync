# qq-group-files-sync
在各群同步文件的小工具，提供一个ui界面

√ 群文件下载
√ 增量同步
√ 展示界面
√ S3 API支持

### 使用

首先按照下方配置文件示例，创建一个 config.yaml
启动一个onebot11的协议端，开发测试于Lagrange.Core v1
随后配置一下 ob11 的反向链接，本地或者s3都可以

程序支持两种启动模式：

#### 1. 命令行同步模式
直接在命令行执行同步操作，完成后自动退出。

**注意**：使用此模式前，请确保在 `config.yaml` 中的 `groups` 部分配置了需要同步的群信息：

```yaml
groups:
  - id: "QQ-Group:12345"
    alias: "示例群1"
    description: "群组描述"
  - id: "QQ-Group:67890"
    alias: "示例群2"
    description: "另一个群组"
```

配置完成后，使用以下命令：

```bash
# 同步所有配置的群组文件
go run . -a
# 或者
go run . -all

# 查看帮助信息
go run . -h
```

#### 2. 交互等待模式
启动后保持运行，通过QQ群消息控制同步：

```bash
# 进入交互等待模式
go run . -i
```

在交互模式下，可以在QQ群内使用以下命令：
```
.同步当前
> 同步当前群的群文件，完成后自动生成展示页面

.同步文件 QQ-Group:群号
> 指定一个群进行同步，账号应该在群内

.同步全部
> 同步 config.yaml 中设置的所有群

.展示页面
> 强制重新生成展示页面
```

#### 同步过程说明

同步启动后，会进行增量下载预测，只进行增量更新。控制台和日志中显示如下:
```cmd
========== 同步预测结果 ==========
群内文件: 7 个文件夹 / 359 个文件 / 总大小 1.8 GB
当前已存: 7 个文件夹 / 200 个文件 / 大小 1011.1 MB
需要更新: 159 个文件 / 下载 799.2 MB
需要创建: 0 个文件夹
需要删除: 0 个文件 / 0 个文件夹 / 释放 0 B
================================
```

注意同步过程中，单条文件的同步情况只会保存在日志中，默认logs/app.log。

在命令行同步模式（`-a` 或 `-all`）下，完成后会显示同步结果统计：
```
========== 同步结果 ==========
成功同步: 5 个群组
失败同步: 0 个群组
展示页面已保存至: ./data/list.html
=============================
```

完成后自动生成展示页面，默认为数据目录下的 list.html，如果是s3的话会自动上传。

示例配置:
```yaml
oneBot11:
  wsReverseUrl: "ws://127.0.0.1:8100/onebot/v11/ws"
  wsForwardAddr: ""

# 本地
# fileSystem:
#   type: "local"
#   localPath: "./data"

# s3
fileSystem:
  type: "s3"
  localPath: ""
  s3Config:
    endpoint: "https://s3.amazonaws.com"
    bucket: "test-bucket"
    accessKey: "test-access-key"
    secretKey: "test-secret-key"
    region: "us-east-1"
    basePath: "files"  # 设置为空字符串表示使用 bucket 根目录，可以设置为 "qq-groups" 等子目录

logFile: "./logs/app.log"

groups:
  - id: "QQ-Group:12345"
    alias: "示例群"
    description: "这是一个示例配置，可在此处为每个群起一个别名"

web:
  title: "群文件导航"
  baseUrl: ""
  dashboardFile: "list.html"
```


### 其他:TODO

- 小文件还是比较多，可能需要多线程下载
- 代理功能，但感觉速度挺快的可能没必要
