package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sealdice/smallseal/adapters"
)

func sanitizeComponent(path string) string {
	unsafeChars := []string{"<", ">", ":", "\"", "|", "?", "*", "/", "\\"}
	for _, char := range unsafeChars {
		path = strings.ReplaceAll(path, char, "_")
	}
	return path
}

func normalizeGroupID(groupID string) string {
	safe := sanitizeComponent(groupID)
	return strings.TrimPrefix(safe, "QQ-Group_")
}

func groupRootDir(groupID string) string {
	normalized := normalizeGroupID(groupID)
	if normalized == "" {
		return "QQ-Group"
	}
	return fmt.Sprintf("QQ-Group_%s", normalized)
}

func groupStatusFilePath(groupID string) string {
	return groupRootDir(groupID) + ".json"
}

func groupTimestampFilePath(groupID string) string {
	return groupRootDir(groupID) + "_timestamps.txt"
}

func groupRelativeFilePath(info *adapters.GroupFileInfo) string {
	fileName := sanitizeComponent(info.FileName)
	if info.FolderPath == "" {
		return fileName
	}
	return filepath.Join(info.FolderPath, fileName)
}

func groupFullFilePath(groupID string, info *adapters.GroupFileInfo) string {
	return filepath.Join(groupRootDir(groupID), groupRelativeFilePath(info))
}
