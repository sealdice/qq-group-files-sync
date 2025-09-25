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
	adapter      adapters.PlatformAdapter
	downloadPath string
	log          *zap.SugaredLogger
}

// NewGroupFileHelper 创建群文件助手
func NewGroupFileHelper(adapter adapters.PlatformAdapter, downloadPath string) *GroupFileHelper {
	return &GroupFileHelper{
		adapter:      adapter,
		downloadPath: downloadPath,
		log:          zap.S().Named("group_file_helper"),
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
		DownloadPath: h.downloadPath,
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
	h.log.Infof("开始下载群 %s 的所有文件到目录: %s", status.GroupID, h.downloadPath)

	// 计算预计的目录大小和文件数量
	totalFiles := len(status.Files)
	totalFolders := len(status.Folders)
	var totalSize int64
	for _, file := range status.Files {
		totalSize += file.FileSize
	}

	// 格式化文件大小
	sizeStr := h.formatFileSize(totalSize)
	h.log.Infof("预计下载: %d 个文件，%d 个文件夹，总大小: %s", totalFiles, totalFolders, sizeStr)

	// 确保下载目录存在，对群号进行标准化处理
	safeGroupID := h.sanitizePath(status.GroupID)
	groupDownloadPath := filepath.Join(h.downloadPath, safeGroupID)
	err := os.MkdirAll(groupDownloadPath, 0755)
	if err != nil {
		return fmt.Errorf("创建下载目录失败: %w", err)
	}

	// 创建文件夹结构
	for _, folder := range status.Folders {
		folderPath := filepath.Join(groupDownloadPath, h.sanitizePath(folder.FolderName))
		err := os.MkdirAll(folderPath, 0755)
		if err != nil {
			h.log.Warnf("创建文件夹 %s 失败: %v", folder.FolderName, err)
		}
	}

	// 下载文件
	successCount := 0
	skipCount := 0
	errorCount := 0

	for _, file := range status.Files {
		// 先检查文件是否已存在且相同
		var filePath string
		if file.FolderPath != "" {
			filePath = filepath.Join(groupDownloadPath, file.FolderPath, h.sanitizePath(file.FileName))
		} else {
			filePath = filepath.Join(groupDownloadPath, h.sanitizePath(file.FileName))
		}

		if h.isFileExistsAndSameWithTimestamp(status.GroupID, filePath, &file) {
			skipCount++
			continue // 跳过已存在且相同的文件
		}

		err := h.downloadSingleFile(status.GroupID, &file, groupDownloadPath)
		if err != nil {
			errorCount++
			h.log.Errorf("下载文件 %s 失败: %v", file.FileName, err)
		} else {
			successCount++
		}
	}

	h.log.Infof("下载完成: 成功 %d 个，跳过 %d 个，失败 %d 个", successCount, skipCount, errorCount)

	// 清理多余的文件
	deletedCount, err := h.cleanupExtraFiles(status, groupDownloadPath)
	if err != nil {
		h.log.Warnf("清理多余文件失败: %v", err)
	} else if deletedCount > 0 {
		h.log.Infof("清理完成: 删除 %d 个多余文件", deletedCount)
	}

	// 保存状态文件
	err = h.saveStatusFile(status, groupDownloadPath)
	if err != nil {
		h.log.Warnf("保存状态文件失败: %v", err)
	}

	return nil
}

// cleanupExtraFiles 清理本地存在但群文件列表中不存在的多余文件
func (h *GroupFileHelper) cleanupExtraFiles(status *GroupFileStatus, groupDownloadPath string) (int, error) {
	deletedCount := 0

	// 创建群文件列表的映射，用于快速查找
	groupFileMap := make(map[string]bool)
	for _, file := range status.Files {
		// 构建文件的完整路径，需要与downloadSingleFile中的路径构建逻辑保持一致
		var filePath string
		if file.FolderPath != "" {
			filePath = filepath.Join(file.FolderPath, h.sanitizePath(file.FileName))
		} else {
			filePath = h.sanitizePath(file.FileName)
		}
		// 标准化路径分隔符
		filePath = filepath.ToSlash(filePath)
		groupFileMap[filePath] = true
	}

	// 递归遍历本地文件夹，查找多余文件
	err := filepath.Walk(groupDownloadPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 跳过目录
		if info.IsDir() {
			return nil
		}

		// 计算相对于群下载目录的路径
		relPath, err := filepath.Rel(groupDownloadPath, path)
		if err != nil {
			h.log.Warnf("计算相对路径失败: %v", err)
			return nil
		}

		// 标准化路径分隔符
		relPath = filepath.ToSlash(relPath)

		// 检查文件是否在群文件列表中
		if !groupFileMap[relPath] {
			// 安全检查：确保文件在群下载目录内
			if !strings.HasPrefix(path, groupDownloadPath) {
				h.log.Warnf("跳过删除目录外文件: %s", path)
				return nil
			}

			// 删除多余文件
			err := os.Remove(path)
			if err != nil {
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

	// 清理空目录
	h.cleanupEmptyDirectories(groupDownloadPath)

	return deletedCount, nil
}

// cleanupEmptyDirectories 清理空目录
func (h *GroupFileHelper) cleanupEmptyDirectories(rootPath string) {
	filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 只处理目录，且不是根目录
		if !info.IsDir() || path == rootPath {
			return nil
		}

		// 检查目录是否为空
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil
		}

		if len(entries) == 0 {
			err := os.Remove(path)
			if err != nil {
				h.log.Warnf("删除空目录 %s 失败: %v", path, err)
			} else {
				h.log.Infof("删除空目录: %s", path)
			}
		}

		return nil
	})
}

// downloadSingleFile 下载单个文件
func (h *GroupFileHelper) downloadSingleFile(groupID string, file *adapters.GroupFileInfo, basePath string) error {
	// 构建文件路径，包含文件夹路径
	var filePath string
	if file.FolderPath != "" {
		// 确保文件夹路径存在
		fullFolderPath := filepath.Join(basePath, file.FolderPath)
		err := os.MkdirAll(fullFolderPath, 0755)
		if err != nil {
			return fmt.Errorf("创建文件夹路径失败: %w", err)
		}
		filePath = filepath.Join(fullFolderPath, h.sanitizePath(file.FileName))
	} else {
		filePath = filepath.Join(basePath, h.sanitizePath(file.FileName))
	}

	// 获取下载链接
	downloadRequest := &adapters.GroupFileDownloadRequest{
		GroupID: groupID,
		FileID:  file.FileID,
		BusID:   file.BusID,
	}

	downloadResponse, err := h.adapter.GroupFileDownload(downloadRequest)
	if err != nil {
		return fmt.Errorf("获取下载链接失败: %w", err)
	}

	// 下载文件
	err = h.downloadFileFromURL(downloadResponse.URL, filePath)
	if err != nil {
		return fmt.Errorf("下载文件失败: %w", err)
	}

	// 设置文件时间
	err = h.setFileTime(filePath, file)
	if err != nil {
		h.log.Warnf("设置文件时间失败: %v", err)
	}

	// 保存时间戳记录
	timestampRecord := FileTimestampRecord{
		FilePath:   filePath,
		FileSize:   file.FileSize,
		ModifyTime: file.ModifyTime,
		UploadTime: file.UploadTime,
		FileID:     file.FileID,
	}
	err = h.saveFileTimestamp(groupID, timestampRecord)
	if err != nil {
		h.log.Warnf("保存时间戳记录失败: %v", err)
	}

	h.log.Infof("文件下载成功: %s", file.FileName)
	return nil
}

// isFileExistsAndSame 检查文件是否存在且相同（通过文件大小和修改时间判断）
// isFileExistsAndSameWithTimestamp 使用时间戳记录检查文件是否存在且相同
func (h *GroupFileHelper) isFileExistsAndSameWithTimestamp(groupID string, filePath string, file *adapters.GroupFileInfo) bool {
	// 检查文件是否存在
	stat, err := os.Stat(filePath)
	if err != nil {
		return false // 文件不存在
	}

	// 检查文件大小
	if stat.Size() != file.FileSize {
		return false
	}

	// 加载时间戳记录
	timestamps, err := h.loadFileTimestamps(groupID)
	if err != nil {
		h.log.Warnf("加载时间戳记录失败: %v", err)
		return false
	}

	// 检查时间戳记录
	record, exists := timestamps[filePath]
	if !exists {
		return false // 没有时间戳记录
	}

	// 比较文件信息
	if record.FileSize != file.FileSize ||
		record.ModifyTime != file.ModifyTime ||
		record.FileID != file.FileID {
		return false
	}

	return true
}

// isFileExistsAndSame 保持原有方法用于向后兼容
func (h *GroupFileHelper) isFileExistsAndSame(filePath string, file *adapters.GroupFileInfo) bool {
	stat, err := os.Stat(filePath)
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
func (h *GroupFileHelper) downloadFileFromURL(url, filePath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("HTTP请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP状态码错误: %d", resp.StatusCode)
	}

	// 创建文件
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("创建文件失败: %w", err)
	}
	defer file.Close()

	// 复制数据
	_, err = io.Copy(file, resp.Body)
	if err != nil {
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
	// 替换不安全的文件名字符
	unsafeChars := []string{"<", ">", ":", "\"", "|", "?", "*", "/", "\\"}
	for _, char := range unsafeChars {
		path = strings.ReplaceAll(path, char, "_")
	}
	return path
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
func (h *GroupFileHelper) saveStatusFile(status *GroupFileStatus, basePath string) error {
	// 获取群组目录名
	groupDirName := filepath.Base(basePath)
	// 状态文件保存到上级目录，以群组目录名命名
	parentDir := filepath.Dir(basePath)
	statusPath := filepath.Join(parentDir, groupDirName+".json")

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化状态失败: %w", err)
	}

	err = os.WriteFile(statusPath, data, 0644)
	if err != nil {
		return fmt.Errorf("写入状态文件失败: %w", err)
	}

	h.log.Infof("状态文件已保存: %s", statusPath)
	return nil
}

// LoadStatusFile 加载状态文件
func (h *GroupFileHelper) LoadStatusFile(groupID string) (*GroupFileStatus, error) {
	// 使用与saveStatusFile相同的逻辑：先清理groupID，然后构建路径
	safeGroupID := h.sanitizePath(groupID)
	// 状态文件在data目录下，以群组目录名命名
	statusPath := filepath.Join(h.downloadPath, safeGroupID+".json")

	data, err := os.ReadFile(statusPath)
	if err != nil {
		return nil, fmt.Errorf("读取状态文件失败: %w", err)
	}

	var status GroupFileStatus
	err = json.Unmarshal(data, &status)
	if err != nil {
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
	// 使用与状态文件相同的sanitizePath处理逻辑
	safeGroupID := h.sanitizePath(groupID)
	return filepath.Join(h.downloadPath, fmt.Sprintf("%s_timestamps.txt", safeGroupID))
}

// loadFileTimestamps 加载文件时间戳记录
func (h *GroupFileHelper) loadFileTimestamps(groupID string) (map[string]FileTimestampRecord, error) {
	timestampFile := h.getTimestampFilePath(groupID)
	timestamps := make(map[string]FileTimestampRecord)

	file, err := os.Open(timestampFile)
	if err != nil {
		if os.IsNotExist(err) {
			return timestamps, nil // 文件不存在，返回空map
		}
		return nil, fmt.Errorf("打开时间戳文件失败: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var record FileTimestampRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			h.log.Warnf("解析时间戳记录失败: %v, 行内容: %s", err, line)
			continue
		}

		// 使用文件路径作为key，后面的记录会覆盖前面的
		timestamps[record.FilePath] = record
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取时间戳文件失败: %w", err)
	}

	return timestamps, nil
}

// saveFileTimestamp 保存单个文件时间戳记录
func (h *GroupFileHelper) saveFileTimestamp(groupID string, record FileTimestampRecord) error {
	timestampFile := h.getTimestampFilePath(groupID)

	// 确保目录存在
	dir := filepath.Dir(timestampFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	// 追加写入
	file, err := os.OpenFile(timestampFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("打开时间戳文件失败: %w", err)
	}
	defer file.Close()

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("序列化时间戳记录失败: %w", err)
	}

	_, err = file.WriteString(string(data) + "\n")
	if err != nil {
		return fmt.Errorf("写入时间戳记录失败: %w", err)
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
		return nil // 没有记录，无需清理
	}

	timestampFile := h.getTimestampFilePath(groupID)
	tempFile := timestampFile + ".tmp"

	// 创建临时文件
	file, err := os.Create(tempFile)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	defer file.Close()

	// 写入去重后的记录
	for _, record := range timestamps {
		data, err := json.Marshal(record)
		if err != nil {
			h.log.Warnf("序列化时间戳记录失败: %v", err)
			continue
		}
		_, err = file.WriteString(string(data) + "\n")
		if err != nil {
			return fmt.Errorf("写入时间戳记录失败: %w", err)
		}
	}

	file.Close()

	// 替换原文件
	if err := os.Rename(tempFile, timestampFile); err != nil {
		return fmt.Errorf("替换时间戳文件失败: %w", err)
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
