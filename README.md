# qq-group-files-sync
在各群同步文件的小工具，提供一个ui界面

√ 群文件下载
√ 增量同步
√ 展示界面
√ S3 API支持

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
