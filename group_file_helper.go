package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sealdice/smallseal/adapters"
	"go.uber.org/zap"
)

// FileTimestampRecord 文件时间戳记录
type FileTimestampRecord struct {
	FilePath   string `json:"file_path"`
	FileSize   int64  `json:"file_size"`
	ModifyTime int64  `json:"modify_time"`
	UploadTime int64  `json:"upload_time"`
	FileID     string `json:"file_id"`
}

// GroupFileStatus 群文件状态信息
type GroupFileStatus struct {
	GroupID      string                     `json:"group_id"`
	LastUpdate   int64                      `json:"last_update"`
	TotalFiles   int                        `json:"total_files"`
	TotalFolders int                        `json:"total_folders"`
	Files        []adapters.GroupFileInfo   `json:"files"`
	Folders      []adapters.GroupFolderInfo `json:"folders"`
	DownloadPath string                     `json:"download_path"`
	Metadata     map[string]interface{}     `json:"metadata,omitempty"`
}

// GroupFileHelper 群文件助手
type GroupFileHelper struct {
	adapter   adapters.PlatformAdapter
	fsManager *FileSystemManager
	log       *zap.SugaredLogger

	// 内存缓存相关字段
	timestampCache map[string]map[string]FileTimestampRecord // groupID -> filePath -> record
	cacheMutex     sync.RWMutex

	// 批量写入相关字段
	pendingWrites map[string][]FileTimestampRecord // groupID -> pending records
	writeCounter  map[string]int                   // groupID -> counter
	writeMutex    sync.Mutex
}

// NewGroupFileHelper 创建群文件助手
func NewGroupFileHelper(adapter adapters.PlatformAdapter, fsManager *FileSystemManager) *GroupFileHelper {
	return &GroupFileHelper{
		adapter:        adapter,
		fsManager:      fsManager,
		log:            zap.S().Named("group_file_helper"),
		timestampCache: make(map[string]map[string]FileTimestampRecord),
		pendingWrites:  make(map[string][]FileTimestampRecord),
		writeCounter:   make(map[string]int),
	}
}

// GetCompleteFileList 获取某个群的完整附件列表（递归获取所有文件夹）
func (h *GroupFileHelper) GetCompleteFileList(groupID string) (*GroupFileStatus, error) {
	h.log.Infof("开始获取群 %s 的完整文件列表", groupID)

	status := &GroupFileStatus{
		GroupID:      groupID,
		LastUpdate:   time.Now().Unix(),
		Files:        make([]adapters.GroupFileInfo, 0),
		Folders:      make([]adapters.GroupFolderInfo, 0),
		DownloadPath: filepath.Join(h.fsManager.GetBasePath(), groupRootDir(groupID)),
		Metadata:     make(map[string]interface{}),
	}

	// 获取根目录文件列表
	err := h.getFileListRecursive(groupID, "", "", status)
	if err != nil {
		return nil, fmt.Errorf("获取文件列表失败: %w", err)
	}

	// 上传者ID保持原样，在需要时可通过FormatStandardUserID函数格式化

	status.TotalFiles = len(status.Files)
	status.TotalFolders = len(status.Folders)

	h.log.Infof("群 %s 文件列表获取完成，共 %d 个文件，%d 个文件夹", groupID, status.TotalFiles, status.TotalFolders)
	return status, nil
}

// getFileListRecursive 递归获取文件列表
func (h *GroupFileHelper) getFileListRecursive(groupID, folderID, folderPath string, status *GroupFileStatus) error {
	request := &adapters.GroupFileListRequest{
		GroupID:  groupID,
		FolderID: folderID,
	}

	response, err := h.adapter.GroupFileList(request)
	if err != nil {
		return fmt.Errorf("获取文件夹 %s 的文件列表失败: %w", folderID, err)
	}

	// 添加文件到状态（去重）
	for _, file := range response.Files {
		if !h.isFileInList(file.FileID, status.Files) {
			// 设置文件的文件夹路径
			file.FolderPath = folderPath
			status.Files = append(status.Files, file)
		}
	}

	// 添加文件夹到状态（去重）
	for _, folder := range response.Folders {
		if !h.isFolderInList(folder.FolderID, status.Folders) {
			status.Folders = append(status.Folders, folder)
		}
	}

	// 递归获取子文件夹
	for _, folder := range response.Folders {
		// 构建子文件夹路径
		subFolderPath := folderPath
		if subFolderPath != "" {
			subFolderPath = filepath.Join(subFolderPath, h.sanitizePath(folder.FolderName))
		} else {
			subFolderPath = h.sanitizePath(folder.FolderName)
		}

		err := h.getFileListRecursive(groupID, folder.FolderID, subFolderPath, status)
		if err != nil {
			h.log.Warnf("获取子文件夹 %s 失败: %v", folder.FolderName, err)
			continue
		}
	}

	return nil
}

// isFileInList 检查文件是否已在列表中
func (h *GroupFileHelper) isFileInList(fileID string, files []adapters.GroupFileInfo) bool {
	for _, file := range files {
		if file.FileID == fileID {
			return true
		}
	}
	return false
}

// isFolderInList 检查文件夹是否已在列表中
func (h *GroupFileHelper) isFolderInList(folderID string, folders []adapters.GroupFolderInfo) bool {
	for _, folder := range folders {
		if folder.FolderID == folderID {
			return true
		}
	}
	return false
}

// formatUploaderID 格式化上传者ID为标准用户ID格式
func (h *GroupFileHelper) formatUploaderID(uploaderQQ int64) int64 {
	// 保持原始QQ号，标准格式化在需要时通过FormatStandardUserID函数处理
	return uploaderQQ
}

// DownloadAllFiles 依次下载所有附件，目录1:1

func (h *GroupFileHelper) DownloadAllFiles(status *GroupFileStatus) error {
	h.log.Infof("开始下载群 %s 的所有文件到目录: %s", status.GroupID, h.fsManager.GetBasePath())

	// 预加载时间戳缓存
	if err := h.preloadTimestampCache(status.GroupID); err != nil {
		h.log.Warnf("预加载时间戳缓存失败: %v", err)
	}

	totalFiles := len(status.Files)
	totalFolders := len(status.Folders)
	var totalSize int64
	for _, file := range status.Files {
		totalSize += file.FileSize
	}

	h.log.Infof("预计下载: %d 个文件，%d 个文件夹，总大小: %s", totalFiles, totalFolders, h.formatFileSize(totalSize))

	groupRoot := groupRootDir(status.GroupID)
	if err := h.fsManager.MkdirAll(groupRoot); err != nil {
		return fmt.Errorf("创建群目录失败: %w", err)
	}

	for _, folder := range status.Folders {
		folderPath := filepath.Join(groupRoot, sanitizeComponent(folder.FolderName))
		if err := h.fsManager.MkdirAll(folderPath); err != nil {
			h.log.Warnf("创建文件夹 %s 失败: %v", folder.FolderName, err)
		}
	}

	successCount := 0
	skipCount := 0
	errorCount := 0

	for _, file := range status.Files {
		relativePath := groupRelativeFilePath(&file)
		targetPath := filepath.Join(groupRoot, relativePath)

		if h.isFileExistsAndSameWithTimestamp(status.GroupID, targetPath, &file) {
			skipCount++
			h.log.Infof("文件 %s/%s 已存在且与群文件相同，跳过下载", file.FolderPath, file.FileName)
			continue
		}

		if err := h.downloadSingleFile(status.GroupID, &file, groupRoot); err != nil {
			errorCount++
			h.log.Errorf("下载文件 %s/%s 失败: %v", file.FolderPath, file.FileName, err)
		} else {
			h.log.Infof("文件下载成功: %s/%s", file.FolderPath, file.FileName)
			successCount++
		}
	}

	h.log.Infof("下载完成: 成功 %d 个，跳过 %d 个，失败 %d 个", successCount, skipCount, errorCount)

	deletedCount, err := h.cleanupExtraFiles(status, groupRoot)
	if err != nil {
		h.log.Warnf("清理多余文件失败: %v", err)
	} else if deletedCount > 0 {
		h.log.Infof("清理完成: 删除 %d 个多余文件", deletedCount)
	}

	if err := h.saveStatusFile(status); err != nil {
		h.log.Warnf("保存状态文件失败: %v", err)
	}

	// 强制写入所有待写入的时间戳记录
	if err := h.FlushAllPendingWrites(); err != nil {
		h.log.Warnf("强制写入时间戳记录失败: %v", err)
	}

	return nil
}

// cleanupExtraFiles 清理本地存在但群文件列表中不存在的多余文件

func (h *GroupFileHelper) cleanupExtraFiles(status *GroupFileStatus, groupRoot string) (int, error) {
	deletedCount := 0

	expected := make(map[string]bool)
	for _, file := range status.Files {
		relPath := filepath.ToSlash(groupRelativeFilePath(&file))
		expected[relPath] = true
	}

	err := h.fsManager.Walk(groupRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(groupRoot, path)
		if err != nil {
			h.log.Warnf("计算相对路径失败: %v", err)
			return nil
		}
		relPath = filepath.ToSlash(relPath)

		if !expected[relPath] {
			if err := h.fsManager.Remove(path); err != nil {
				h.log.Warnf("删除多余文件 %s 失败: %v", relPath, err)
			} else {
				h.log.Infof("删除多余文件: %s", relPath)
				deletedCount++
			}
		}

		return nil
	})

	if err != nil {
		return deletedCount, fmt.Errorf("遍历目录失败: %w", err)
	}

	h.cleanupEmptyDirectories(groupRoot)

	return deletedCount, nil
}

// cleanupEmptyDirectories 清理空目录

func (h *GroupFileHelper) cleanupEmptyDirectories(rootPath string) {
	h.fsManager.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() || path == rootPath {
			return nil
		}

		entries, readErr := h.fsManager.ReadDir(path)
		if readErr != nil {
			return nil
		}

		if len(entries) == 0 {
			if err := h.fsManager.RemoveAll(path); err != nil {
				h.log.Warnf("移除空目录 %s 失败: %v", path, err)
			} else {
				h.log.Infof("移除空目录: %s", path)
			}
		}

		return nil
	})
}

// downloadSingleFile 下载单个文件

func (h *GroupFileHelper) downloadSingleFile(groupID string, file *adapters.GroupFileInfo, baseRoot string) error {
	relativePath := groupRelativeFilePath(file)
	targetPath := filepath.Join(baseRoot, relativePath)

	dir := filepath.Dir(targetPath)
	if dir != "." && dir != "" {
		if err := h.fsManager.MkdirAll(dir); err != nil {
			return fmt.Errorf("创建文件夹路径失败: %w", err)
		}
	}

	downloadRequest := &adapters.GroupFileDownloadRequest{
		GroupID: groupID,
		FileID:  file.FileID,
		BusID:   file.BusID,
	}

	downloadResponse, err := h.adapter.GroupFileDownload(downloadRequest)
	if err != nil {
		return fmt.Errorf("获取下载链接失败: %w", err)
	}

	if err := h.downloadFileFromURLToFS(downloadResponse.URL, targetPath); err != nil {
		return fmt.Errorf("下载文件失败: %w", err)
	}

	if base := h.fsManager.GetBasePath(); base != "" {
		fullPath := filepath.Join(base, targetPath)
		if err := h.setFileTime(fullPath, file); err != nil {
			h.log.Warnf("设置文件时间失败: %v", err)
		}
	}

	timestampRecord := FileTimestampRecord{
		FilePath:   filepath.ToSlash(targetPath),
		FileSize:   file.FileSize,
		ModifyTime: file.ModifyTime,
		UploadTime: file.UploadTime,
		FileID:     file.FileID,
	}
	if err := h.saveFileTimestamp(groupID, timestampRecord); err != nil {
		h.log.Warnf("保存时间戳记录失败: %v", err)
	}

	return nil
}

// isFileExistsAndSame 检查文件是否存在且相同（通过文件大小和修改时间判断）
// isFileExistsAndSameWithTimestamp 使用时间戳记录检查文件是否存在且相同

func (h *GroupFileHelper) isFileExistsAndSameWithTimestamp(groupID string, filePath string, file *adapters.GroupFileInfo) bool {
	stat, err := h.fsManager.Stat(filePath)
	if err != nil {
		return false
	}

	if stat.Size() != file.FileSize {
		return false
	}

	// 使用内存缓存而不是每次读取文件
	record, exists := h.getTimestampFromCache(groupID, filePath)
	if !exists {
		return false
	}

	if record.FileSize != file.FileSize || record.ModifyTime != file.ModifyTime || record.FileID != file.FileID {
		return false
	}

	return true
}

// isFileExistsAndSame 保持原有方法用于向后兼容
func (h *GroupFileHelper) isFileExistsAndSame(filePath string, file *adapters.GroupFileInfo) bool {
	stat, err := h.fsManager.Stat(filePath)
	if err != nil {
		return false // 文件不存在
	}

	// 检查文件大小
	if stat.Size() != file.FileSize {
		return false
	}

	// 检查修改时间（允许1秒误差）
	fileModTime := time.Unix(file.ModifyTime, 0)
	if abs(stat.ModTime().Unix()-fileModTime.Unix()) > 1 {
		return false
	}

	return true
}

// downloadFileFromURL 从URL下载文件

func (h *GroupFileHelper) downloadFileFromURLToFS(url, filePath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("HTTP请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP状态码错误: %d", resp.StatusCode)
	}

	file, err := h.fsManager.Create(filePath)
	if err != nil {
		return fmt.Errorf("创建文件失败: %w", err)
	}
	defer file.Close()

	if _, err := io.Copy(file, resp.Body); err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}

	return nil
}

// setFileTime 设置文件的修改时间和创建时间
func (h *GroupFileHelper) setFileTime(filePath string, file *adapters.GroupFileInfo) error {
	uploadTime := time.Unix(file.UploadTime, 0)
	modifyTime := time.Unix(file.ModifyTime, 0)

	// 设置访问时间和修改时间
	err := os.Chtimes(filePath, uploadTime, modifyTime)
	if err != nil {
		return fmt.Errorf("设置文件时间失败: %w", err)
	}

	return nil
}

// sanitizePath 清理路径，移除不安全字符
func (h *GroupFileHelper) sanitizePath(path string) string {
	return sanitizeComponent(path)
}

// formatFileSize 格式化文件大小为人类可读的格式
func (h *GroupFileHelper) formatFileSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

// saveStatusFile 保存状态文件

func (h *GroupFileHelper) saveStatusFile(status *GroupFileStatus) error {
	statusPath := groupStatusFilePath(status.GroupID)

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化状态失败: %w", err)
	}

	if err := h.fsManager.WriteFile(statusPath, data); err != nil {
		return fmt.Errorf("写入状态文件失败: %w", err)
	}

	h.log.Infof("状态文件已保存: %s", statusPath)
	return nil
}

// LoadStatusFile 加载状态文件

func (h *GroupFileHelper) LoadStatusFile(groupID string) (*GroupFileStatus, error) {
	statusPath := groupStatusFilePath(groupID)
	data, err := h.fsManager.ReadFile(statusPath)
	if err != nil {
		return nil, fmt.Errorf("读取状态文件失败: %w", err)
	}

	var status GroupFileStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("解析状态文件失败: %w", err)
	}

	return &status, nil
}

// abs 返回整数的绝对值
func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// FormatStandardUserID 格式化为标准用户ID格式
func FormatStandardUserID(platform string, userID int64) string {
	return fmt.Sprintf("%s:%s", platform, strconv.FormatInt(userID, 10))
}

// SyncGroupFiles 同步群文件（获取列表并下载）
// getTimestampFilePath 获取时间戳文件路径
func (h *GroupFileHelper) getTimestampFilePath(groupID string) string {
	return groupTimestampFilePath(groupID)
}

// loadFileTimestamps 加载文件时间戳记录

func (h *GroupFileHelper) loadFileTimestamps(groupID string) (map[string]FileTimestampRecord, error) {
	timestampFile := h.getTimestampFilePath(groupID)
	timestamps := make(map[string]FileTimestampRecord)

	data, err := h.fsManager.ReadFile(timestampFile)
	if err != nil {
		if h.fsManager.IsNotExist(err) {
			return timestamps, nil
		}
		return nil, fmt.Errorf("读取时间戳文件失败: %w", err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var record FileTimestampRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			h.log.Warnf("解析时间戳记录失败: %v, 原始行: %s", err, line)
			continue
		}

		key := filepath.ToSlash(record.FilePath)
		record.FilePath = key
		timestamps[key] = record
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取时间戳文件失败: %w", err)
	}

	return timestamps, nil
}

// preloadTimestampCache 预加载指定群的时间戳缓存
func (h *GroupFileHelper) preloadTimestampCache(groupID string) error {
	h.cacheMutex.Lock()
	defer h.cacheMutex.Unlock()

	timestamps, err := h.loadFileTimestamps(groupID)
	if err != nil {
		return fmt.Errorf("预加载时间戳缓存失败: %w", err)
	}

	h.timestampCache[groupID] = timestamps
	h.log.Infof("已预加载群 %s 的时间戳缓存，共 %d 条记录", groupID, len(timestamps))
	return nil
}

// getTimestampFromCache 从缓存中获取时间戳记录
func (h *GroupFileHelper) getTimestampFromCache(groupID string, filePath string) (FileTimestampRecord, bool) {
	h.cacheMutex.RLock()
	defer h.cacheMutex.RUnlock()

	groupCache, exists := h.timestampCache[groupID]
	if !exists {
		return FileTimestampRecord{}, false
	}

	key := filepath.ToSlash(filePath)
	record, exists := groupCache[key]
	return record, exists
}

// updateTimestampCache 更新缓存中的时间戳记录
func (h *GroupFileHelper) updateTimestampCache(groupID string, record FileTimestampRecord) {
	h.cacheMutex.Lock()
	defer h.cacheMutex.Unlock()

	if h.timestampCache[groupID] == nil {
		h.timestampCache[groupID] = make(map[string]FileTimestampRecord)
	}

	key := filepath.ToSlash(record.FilePath)
	record.FilePath = key
	h.timestampCache[groupID][key] = record
}

// saveFileTimestamp 保存单个文件时间戳记录（批量写入，每10条写入一次）
func (h *GroupFileHelper) saveFileTimestamp(groupID string, record FileTimestampRecord) error {
	h.writeMutex.Lock()
	defer h.writeMutex.Unlock()

	// 更新内存缓存
	h.updateTimestampCache(groupID, record)

	// 添加到待写入队列
	if h.pendingWrites[groupID] == nil {
		h.pendingWrites[groupID] = make([]FileTimestampRecord, 0)
		h.writeCounter[groupID] = 0
	}

	h.pendingWrites[groupID] = append(h.pendingWrites[groupID], record)
	h.writeCounter[groupID]++

	// 每10条记录写入一次
	if h.writeCounter[groupID] >= 10 {
		return h.flushPendingWrites(groupID)
	}

	return nil
}

// flushPendingWrites 强制写入所有待写入的记录
func (h *GroupFileHelper) flushPendingWrites(groupID string) error {
	if len(h.pendingWrites[groupID]) == 0 {
		return nil
	}

	timestampFile := h.getTimestampFilePath(groupID)

	// 从缓存中获取所有记录
	h.cacheMutex.RLock()
	timestamps := h.timestampCache[groupID]
	h.cacheMutex.RUnlock()

	if timestamps == nil {
		timestamps = make(map[string]FileTimestampRecord)
	}

	var builder strings.Builder
	for _, rec := range timestamps {
		data, err := json.Marshal(rec)
		if err != nil {
			h.log.Warnf("序列化时间戳记录失败: %v", err)
			continue
		}
		builder.WriteString(string(data))
		builder.WriteByte('\n')
	}

	if err := h.fsManager.WriteFile(timestampFile, []byte(builder.String())); err != nil {
		return fmt.Errorf("批量写入时间戳记录失败: %w", err)
	}

	// 清空待写入队列
	h.pendingWrites[groupID] = h.pendingWrites[groupID][:0]
	h.writeCounter[groupID] = 0

	h.log.Debugf("已批量写入群 %s 的时间戳记录到文件", groupID)
	return nil
}

// FlushAllPendingWrites 强制写入所有群的待写入记录（用于程序退出时）
func (h *GroupFileHelper) FlushAllPendingWrites() error {
	h.writeMutex.Lock()
	defer h.writeMutex.Unlock()

	for groupID := range h.pendingWrites {
		if err := h.flushPendingWrites(groupID); err != nil {
			h.log.Errorf("强制写入群 %s 的时间戳记录失败: %v", groupID, err)
			return err
		}
	}

	return nil
}

// cleanupTimestampFile 清理时间戳文件中的重复记录

func (h *GroupFileHelper) cleanupTimestampFile(groupID string) error {
	timestamps, err := h.loadFileTimestamps(groupID)
	if err != nil {
		return fmt.Errorf("加载时间戳记录失败: %w", err)
	}

	if len(timestamps) == 0 {
		return nil
	}

	var builder strings.Builder
	for _, record := range timestamps {
		data, err := json.Marshal(record)
		if err != nil {
			h.log.Warnf("序列化时间戳记录失败: %v", err)
			continue
		}
		builder.WriteString(string(data))
		builder.WriteByte('\n')
	}

	timestampFile := h.getTimestampFilePath(groupID)
	if err := h.fsManager.WriteFile(timestampFile, []byte(builder.String())); err != nil {
		return fmt.Errorf("写入时间戳文件失败: %w", err)
	}

	h.log.Infof("时间戳文件清理完成，共保留 %d 条记录", len(timestamps))
	return nil
}

func (h *GroupFileHelper) SyncGroupFiles(groupID string) error {
	// 获取完整文件列表
	status, err := h.GetCompleteFileList(groupID)
	if err != nil {
		return fmt.Errorf("获取文件列表失败: %w", err)
	}

	// 下载所有文件
	err = h.DownloadAllFiles(status)
	if err != nil {
		return fmt.Errorf("下载文件失败: %w", err)
	}

	// 清理时间戳文件中的重复记录
	err = h.cleanupTimestampFile(groupID)
	if err != nil {
		h.log.Warnf("清理时间戳文件失败: %v", err)
	}

	return nil
}
