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

在想要同步的群输入："同步文件" 即可开始同步
完成后输入 "展示页面" 即可生成展示页面，数据目录下的 list.html 如果是s3的话会自动上传

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


### 其他

- 小文件还是比较多，可能需要多线程下载
- 代理功能，但感觉速度挺快的可能没必要
