package ingest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// ImageStore 识别原图的临时存储。Phase 0 为本地磁盘；接入 TOS 时只换实现（ADR-006 修订记录）。
type ImageStore interface {
	Put(familyID, jobID uint64, seq int, data []byte) error
	Get(familyID, jobID uint64, seq int) ([]byte, error)
	DeleteJob(familyID, jobID uint64) error
	// CleanupOlderThan 删除修改时间早于 cutoff 的残留任务目录，返回删除数。
	CleanupOlderThan(cutoff time.Time) (int, error)
}

// LocalImageStore 本地临时目录实现：{root}/{family_id}/{job_id}/{seq}.img。
// 路径全部由服务端数字 ID 拼接，不使用客户端文件名，杜绝路径穿越。
type LocalImageStore struct {
	root string
}

// NewLocalImageStore 确保根目录存在（权限 700）。
func NewLocalImageStore(root string) (*LocalImageStore, error) {
	if root == "" {
		return nil, errors.New("INGEST_DIR 未配置")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("创建 INGEST_DIR 失败: %w", err)
	}
	return &LocalImageStore{root: root}, nil
}

func (s *LocalImageStore) jobDir(familyID, jobID uint64) string {
	return filepath.Join(s.root, strconv.FormatUint(familyID, 10), strconv.FormatUint(jobID, 10))
}

func (s *LocalImageStore) path(familyID, jobID uint64, seq int) string {
	return filepath.Join(s.jobDir(familyID, jobID), strconv.Itoa(seq)+".img")
}

func (s *LocalImageStore) Put(familyID, jobID uint64, seq int, data []byte) error {
	dir := s.jobDir(familyID, jobID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := s.path(familyID, jobID, seq) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(familyID, jobID, seq))
}

func (s *LocalImageStore) Get(familyID, jobID uint64, seq int) ([]byte, error) {
	return os.ReadFile(s.path(familyID, jobID, seq))
}

func (s *LocalImageStore) DeleteJob(familyID, jobID uint64) error {
	return os.RemoveAll(s.jobDir(familyID, jobID))
}

func (s *LocalImageStore) CleanupOlderThan(cutoff time.Time) (int, error) {
	families, err := os.ReadDir(s.root)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, f := range families {
		if !f.IsDir() {
			continue
		}
		famDir := filepath.Join(s.root, f.Name())
		jobs, err := os.ReadDir(famDir)
		if err != nil {
			continue
		}
		for _, j := range jobs {
			info, err := j.Info()
			if err != nil || !j.IsDir() || info.ModTime().After(cutoff) {
				continue
			}
			if os.RemoveAll(filepath.Join(famDir, j.Name())) == nil {
				n++
			}
		}
		if rest, err := os.ReadDir(famDir); err == nil && len(rest) == 0 {
			_ = os.Remove(famDir)
		}
	}
	return n, nil
}

// detectImageMIME 按文件头识别格式，只接受 JPEG / PNG / WebP。
func detectImageMIME(b []byte) (string, bool) {
	switch {
	case len(b) >= 3 && bytes.Equal(b[:3], []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg", true
	case len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}):
		return "image/png", true
	case len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp", true
	}
	return "", false
}
