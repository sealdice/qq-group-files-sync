package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	aferos3 "github.com/fclairamb/afero-s3"
	"github.com/spf13/afero"
	"go.uber.org/zap"
)

// FileSystemManager wraps the underlying storage so the rest of the code can
// remain unaware of local/S3 differences. For local storage we isolate paths
// inside the configured base directory; for S3 we normalise keys to POSIX
// style and avoid creating placeholder "directories".
type FileSystemManager struct {
	fs            afero.Fs
	root          string
	kind          string
	log           *zap.SugaredLogger
	s3Session     *session.Session
	s3Config      *S3Config
	s3BasePath    string
	s3ListObjects func(*session.Session, string, string) ([]string, error)
}

// NewFileSystemManager creates a new manager based on configuration.
func NewFileSystemManager(config FileSystemConfig) (*FileSystemManager, error) {
	log := zap.S().Named("filesystem")

	manager := &FileSystemManager{
		kind: config.Type,
		log:  log,
	}

	switch config.Type {
	case "local":
		basePath := config.LocalPath
		if basePath == "" {
			basePath = "."
		}
		if err := os.MkdirAll(basePath, 0755); err != nil {
			return nil, fmt.Errorf("初始化本地目录失败: %w", err)
		}
		manager.fs = afero.NewBasePathFs(afero.NewOsFs(), basePath)
		manager.root = basePath
		log.Infof("使用本地文件系统，路径: %s", basePath)
	case "s3":
		fs, sess, err := createS3FileSystem(config.S3Config)
		if err != nil {
			return nil, fmt.Errorf("创建S3文件系统失败: %w", err)
		}
		manager.fs = fs
		cfgCopy := config.S3Config
		manager.s3Session = sess
		manager.s3Config = &cfgCopy
		manager.s3BasePath = strings.Trim(config.S3Config.BasePath, "/")
		manager.s3ListObjects = defaultS3ListObjects
		if manager.s3BasePath != "" {
			log.Infof("使用S3文件系统，Bucket: %s, 基础路径: %s", config.S3Config.Bucket, manager.s3BasePath)
		} else {
			log.Infof("使用S3文件系统，Bucket: %s", config.S3Config.Bucket)
		}
	default:
		return nil, fmt.Errorf("不支持的文件系统类型: %s", config.Type)
	}

	return manager, nil
}

// createS3FileSystem instantiates an afero-backed S3 filesystem.
func createS3FileSystem(config S3Config) (afero.Fs, *session.Session, error) {
	sess, err := session.NewSession(&aws.Config{
		Region:           aws.String(config.Region),
		Endpoint:         aws.String(config.Endpoint),
		DisableSSL:       aws.Bool(!config.UseSSL),
		S3ForcePathStyle: aws.Bool(true),
		Credentials:      credentials.NewStaticCredentials(config.AccessKey, config.SecretKey, ""),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("创建AWS会话失败: %w", err)
	}

	fs := aferos3.NewFs(config.Bucket, sess)
	return fs, sess, nil
}

// GetBasePath returns the root directory for local storage (empty for S3).
func (fsm *FileSystemManager) GetBasePath() string {
	if fsm.kind == "local" {
		return fsm.root
	}
	return ""
}

func (fsm *FileSystemManager) normalizePath(p string) string {
	if p == "" {
		return ""
	}
	cleaned := filepath.Clean(p)
	if cleaned == "." {
		return ""
	}
	if fsm.kind == "local" {
		return cleaned
	}
	// S3 路径处理
	cleaned = strings.ReplaceAll(cleaned, "\\", "/")
	for strings.HasPrefix(cleaned, "./") {
		cleaned = strings.TrimPrefix(cleaned, "./")
	}
	cleaned = strings.TrimPrefix(cleaned, "/")

	// 如果设置了 S3 基础路径，则添加前缀
	if fsm.s3BasePath != "" && cleaned != "" {
		cleaned = fsm.s3BasePath + "/" + cleaned
	} else if fsm.s3BasePath != "" {
		cleaned = fsm.s3BasePath
	}

	return cleaned
}

func (fsm *FileSystemManager) ensureDir(dir string) error {
	if dir == "" {
		return nil
	}
	if fsm.kind != "local" {
		return nil
	}
	return fsm.fs.MkdirAll(dir, 0755)
}

// WriteFile writes data to filename, creating parent directories when needed.
func (fsm *FileSystemManager) WriteFile(filename string, data []byte) error {
	norm := fsm.normalizePath(filename)
	dir := filepath.Dir(norm)
	if dir == "." {
		dir = ""
	}
	if err := fsm.ensureDir(dir); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	if err := afero.WriteFile(fsm.fs, norm, data, 0644); err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}
	return nil
}

// ReadFile reads the file at filename.
func (fsm *FileSystemManager) ReadFile(filename string) ([]byte, error) {
	norm := fsm.normalizePath(filename)
	return afero.ReadFile(fsm.fs, norm)
}

// Exists checks whether a file or directory exists.
func (fsm *FileSystemManager) Exists(filename string) (bool, error) {
	norm := fsm.normalizePath(filename)
	return afero.Exists(fsm.fs, norm)
}

// Stat returns the file information for filename.
func (fsm *FileSystemManager) Stat(filename string) (os.FileInfo, error) {
	norm := fsm.normalizePath(filename)
	return fsm.fs.Stat(norm)
}

// Remove deletes a file.
func (fsm *FileSystemManager) Remove(filename string) error {
	norm := fsm.normalizePath(filename)
	return fsm.fs.Remove(norm)
}

// RemoveAll removes a directory tree.
func (fsm *FileSystemManager) RemoveAll(path string) error {
	norm := fsm.normalizePath(path)
	if norm == "" {
		return nil
	}
	return fsm.fs.RemoveAll(norm)
}

// MkdirAll creates a directory tree when running on local storage.
func (fsm *FileSystemManager) MkdirAll(path string) error {
	norm := fsm.normalizePath(path)
	if norm == "" {
		return nil
	}
	if fsm.kind != "local" {
		return nil
	}
	return fsm.fs.MkdirAll(norm, 0755)
}

// OpenFile opens a file with the requested flags.
func (fsm *FileSystemManager) OpenFile(filename string, flag int, perm os.FileMode) (afero.File, error) {
	norm := fsm.normalizePath(filename)
	return fsm.fs.OpenFile(norm, flag, perm)
}

// Create creates a new file, ensuring the parent directory exists when needed.
func (fsm *FileSystemManager) Create(filename string) (afero.File, error) {
	norm := fsm.normalizePath(filename)
	dir := filepath.Dir(norm)
	if dir == "." {
		dir = ""
	}
	if err := fsm.ensureDir(dir); err != nil {
		return nil, fmt.Errorf("创建目录失败: %w", err)
	}
	return fsm.fs.Create(norm)
}

// Walk traverses the filesystem starting at root.
func (fsm *FileSystemManager) Walk(root string, walkFn filepath.WalkFunc) error {
	norm := fsm.normalizePath(root)
	if fsm.kind == "s3" && norm == "" {
		return afero.Walk(fsm.fs, ".", func(path string, info os.FileInfo, err error) error {
			if path == "." {
				return walkFn("", info, err)
			}
			next := strings.TrimPrefix(filepath.ToSlash(path), "./")
			return walkFn(next, info, err)
		})
	}
	return afero.Walk(fsm.fs, norm, walkFn)
}

// ListStatusFiles returns all persisted group status files (relative path or S3 key).
func (fsm *FileSystemManager) ListStatusFiles() ([]string, error) {
	if fsm.kind == "s3" {
		return fsm.listS3StatusFiles()
	}
	return fsm.listLocalStatusFiles(".")
}

func (fsm *FileSystemManager) listLocalStatusFiles(pathExtra string) ([]string, error) {
	files := make([]string, 0)
	err := afero.Walk(fsm.fs, pathExtra, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		name := info.Name()
		// 不知道这个过滤干啥的，感觉没必要的样子
		if !strings.HasPrefix(name, "QQ-Group_") || !strings.HasSuffix(strings.ToLower(name), ".json") {
			return nil
		}
		normalized := strings.TrimPrefix(filepath.ToSlash(path), "./")
		if normalized == "" {
			normalized = name
		}
		files = append(files, normalized)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (fsm *FileSystemManager) listS3StatusFiles() ([]string, error) {
	if fsm.s3Session == nil || fsm.s3Config == nil || fsm.s3ListObjects == nil {
		return nil, fmt.Errorf("S3文件系统尚未初始化")
	}

	// 构建搜索前缀，考虑基础路径
	searchPrefix := "QQ-Group_"
	if fsm.s3BasePath != "" {
		searchPrefix = fsm.s3BasePath + "/" + searchPrefix
	}

	keys, err := fsm.s3ListObjects(fsm.s3Session, fsm.s3Config.Bucket, searchPrefix)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(keys))
	for _, key := range keys {
		// 移除基础路径前缀以获得相对路径
		relativeKey := key
		if fsm.s3BasePath != "" {
			if strings.HasPrefix(key, fsm.s3BasePath+"/") {
				relativeKey = strings.TrimPrefix(key, fsm.s3BasePath+"/")
			} else {
				continue // 跳过不在基础路径下的文件
			}
		}

		if !strings.HasPrefix(relativeKey, "QQ-Group_") {
			continue
		}
		lower := strings.ToLower(relativeKey)
		if !strings.HasSuffix(lower, ".json") {
			continue
		}
		suffix := strings.TrimPrefix(relativeKey, "QQ-Group_")
		if strings.Contains(suffix, "/") {
			continue
		}
		files = append(files, relativeKey)
	}
	return files, nil
}

// 直接列出整个路径的文件列表
func (fsm *FileSystemManager) ListStatusFilesX(pathExtra string) ([]string, error) {
	if fsm.kind == "s3" {
		return fsm.listS3StatusFilesX(pathExtra)
	}
	return fsm.listLocalStatusFiles(pathExtra)
}

func (fsm *FileSystemManager) listS3StatusFilesX(pathExtra string) ([]string, error) {
	if fsm.s3Session == nil || fsm.s3Config == nil || fsm.s3ListObjects == nil {
		return nil, fmt.Errorf("S3文件系统尚未初始化")
	}

	// 构建搜索前缀，考虑基础路径
	searchPrefix := pathExtra
	if fsm.s3BasePath != "" {
		searchPrefix = fsm.s3BasePath + "/" + pathExtra
	}
	// 如果不以 / 结尾，加上
	if !strings.HasSuffix(searchPrefix, "/") {
		searchPrefix += "/"
	}

	keys, err := fsm.s3ListObjects(fsm.s3Session, fsm.s3Config.Bucket, searchPrefix)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(keys))
	for _, key := range keys {
		// 移除基础路径前缀以获得相对路径
		relativeKey := key
		if fsm.s3BasePath != "" {
			if strings.HasPrefix(key, fsm.s3BasePath+"/") {
				relativeKey = strings.TrimPrefix(key, fsm.s3BasePath+"/")
			} else {
				continue // 跳过不在基础路径下的文件
			}
		}
		files = append(files, relativeKey)
	}
	return files, nil
}

func defaultS3ListObjects(sess *session.Session, bucket string, prefix string) ([]string, error) {
	svc := s3.New(sess)
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	}
	results := make([]string, 0)
	for {
		out, err := svc.ListObjectsV2(input)
		if err != nil {
			return nil, fmt.Errorf("列出S3对象失败: %w", err)
		}
		for _, obj := range out.Contents {
			key := aws.StringValue(obj.Key)
			if key == "" {
				continue
			}
			results = append(results, key)
		}
		if out.IsTruncated != nil && *out.IsTruncated {
			input.ContinuationToken = out.NextContinuationToken
		} else {
			break
		}
	}
	return results, nil
}

// ReadDir lists the contents of a directory.
func (fsm *FileSystemManager) ReadDir(dirname string) ([]os.FileInfo, error) {
	norm := fsm.normalizePath(dirname)
	return afero.ReadDir(fsm.fs, norm)
}

// IsNotExist reports whether err indicates that a file does not exist.
func (fsm *FileSystemManager) IsNotExist(err error) bool {
	return os.IsNotExist(err)
}

// GetFileSystem exposes the underlying afero filesystem (for advanced usage).
func (fsm *FileSystemManager) GetFileSystem() afero.Fs {
	return fsm.fs
}
