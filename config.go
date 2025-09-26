package main

import (
	"fmt"
	"os"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
	"github.com/samber/lo"
)

type OneBot11Config struct {
	WSReverseURL  string `json:"wsReverseUrl" yaml:"wsReverseUrl" koanf:"wsReverseUrl"`
	WSForwardAddr string `json:"wsForwardAddr" yaml:"wsForwardAddr" koanf:"wsForwardAddr"`
	AccessToken   string `json:"accessToken" yaml:"accessToken" koanf:"accessToken"`
	Secret        string `json:"secret" yaml:"secret" koanf:"secret"`
}

type S3Config struct {
	Endpoint  string `json:"endpoint" yaml:"endpoint" koanf:"endpoint"`
	Bucket    string `json:"bucket" yaml:"bucket" koanf:"bucket"`
	AccessKey string `json:"accessKey" yaml:"accessKey" koanf:"accessKey"`
	SecretKey string `json:"secretKey" yaml:"secretKey" koanf:"secretKey"`
	Region    string `json:"region" yaml:"region" koanf:"region"`
	UseSSL    bool   `json:"useSSL" yaml:"useSSL" koanf:"useSSL"`
	BasePath  string `json:"basePath" yaml:"basePath" koanf:"basePath"`
}

type FileSystemConfig struct {
	Type      string   `json:"type" yaml:"type" koanf:"type"`
	LocalPath string   `json:"localPath" yaml:"localPath" koanf:"localPath"`
	S3Config  S3Config `json:"s3Config" yaml:"s3Config" koanf:"s3Config"`
}

type GroupConfig struct {
	ID          string `json:"id" yaml:"id" koanf:"id"`
	Alias       string `json:"alias" yaml:"alias" koanf:"alias"`
	Description string `json:"description" yaml:"description" koanf:"description"`
}

type WebConfig struct {
	Title         string `json:"title" yaml:"title" koanf:"title"`
	BaseURL       string `json:"baseUrl" yaml:"baseUrl" koanf:"baseUrl"`
	DashboardFile string `json:"dashboardFile" yaml:"dashboardFile" koanf:"dashboardFile"`
}

type AppConfig struct {
	OneBot11Config   OneBot11Config   `json:"oneBot11" yaml:"oneBot11" koanf:"oneBot11"`
	FileSystemConfig FileSystemConfig `json:"fileSystem" yaml:"fileSystem" koanf:"fileSystem"`
	LogFile          string           `json:"logFile" yaml:"logFile" koanf:"logFile"`
	Groups           []GroupConfig    `json:"groups" yaml:"groups" koanf:"groups"`
	Web              WebConfig        `json:"web" yaml:"web" koanf:"web"`
}

var k = koanf.New(".")

func ReadConfig() *AppConfig {
	config := AppConfig{
		OneBot11Config: OneBot11Config{
			WSReverseURL:  "ws://127.0.0.1:8100/onebot/v11/ws",
			WSForwardAddr: "",
			AccessToken:   "",
			Secret:        "",
		},
		FileSystemConfig: FileSystemConfig{
			Type:      "local",
			LocalPath: "./test_data",
			S3Config: S3Config{
				Endpoint:  "",
				Bucket:    "",
				AccessKey: "",
				SecretKey: "",
				Region:    "us-east-1",
				UseSSL:    true,
			},
		},
		LogFile: "./logs/app.log",
		Groups: []GroupConfig{
			{
				ID:          "QQ-Group:578800173",
				Alias:       "示例群",
				Description: "这是一个示例配置，可在此处为每个群起一个别名",
			},
		},
		Web: WebConfig{
			Title:         "群文件导航",
			BaseURL:       "",
			DashboardFile: "index.html",
		},
	}

	lo.Must0(k.Load(structs.Provider(&config, "yaml"), nil))

	f := file.Provider("config.yaml")

	isNotExist := false
	if err := k.Load(f, yaml.Parser()); err != nil {
		fmt.Printf("配置读取失败: %v\n", err)

		if os.IsNotExist(err) {
			isNotExist = true
		} else {
			os.Exit(-1)
		}
	}

	if isNotExist {
		WriteConfig(&config)
	} else {
		if err := k.Unmarshal("", &config); err != nil {
			fmt.Printf("配置解析失败: %v\n", err)
			os.Exit(-1)
		}
	}

	if config.Web.DashboardFile == "" {
		config.Web.DashboardFile = "index.html"
	}
	if config.Web.Title == "" {
		config.Web.Title = "群文件导航"
	}

	k.Print()
	return &config
}

func WriteConfig(config *AppConfig) {
	if config != nil {
		if config.OneBot11Config.WSReverseURL != "" {
			_ = k.Set("oneBot11.wsReverseUrl", config.OneBot11Config.WSReverseURL)
		}
		if config.OneBot11Config.WSForwardAddr != "" {
			_ = k.Set("oneBot11.wsForwardAddr", config.OneBot11Config.WSForwardAddr)
		}
		if config.OneBot11Config.AccessToken != "" {
			_ = k.Set("oneBot11.accessToken", config.OneBot11Config.AccessToken)
		}
		if config.OneBot11Config.Secret != "" {
			_ = k.Set("oneBot11.secret", config.OneBot11Config.Secret)
		}

		_ = k.Set("fileSystem.type", config.FileSystemConfig.Type)
		_ = k.Set("fileSystem.localPath", config.FileSystemConfig.LocalPath)
		_ = k.Set("fileSystem.s3Config.endpoint", config.FileSystemConfig.S3Config.Endpoint)
		_ = k.Set("fileSystem.s3Config.bucket", config.FileSystemConfig.S3Config.Bucket)
		_ = k.Set("fileSystem.s3Config.accessKey", config.FileSystemConfig.S3Config.AccessKey)
		_ = k.Set("fileSystem.s3Config.secretKey", config.FileSystemConfig.S3Config.SecretKey)
		_ = k.Set("fileSystem.s3Config.region", config.FileSystemConfig.S3Config.Region)
		_ = k.Set("fileSystem.s3Config.useSSL", config.FileSystemConfig.S3Config.UseSSL)

		if config.LogFile != "" {
			_ = k.Set("logFile", config.LogFile)
		}

		_ = k.Set("groups", config.Groups)
		_ = k.Set("web.title", config.Web.Title)
		_ = k.Set("web.baseUrl", config.Web.BaseURL)
		_ = k.Set("web.dashboardFile", config.Web.DashboardFile)

		if err := k.Unmarshal("", &config); err != nil {
			fmt.Printf("配置解析失败: %v\n", err)
			os.Exit(-1)
		}
	}

	content, err := yaml.Parser().Marshal(k.Raw())
	if err != nil {
		fmt.Println("错误: 配置文件序列化失败")
		return
	}
	err = os.WriteFile("./config.yaml", content, 0644)
	if err != nil {
		fmt.Println("错误: 配置文件写入失败")
	}
}
