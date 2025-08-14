package network

import (
	"sync"

	"go.uber.org/zap"
)

type Manager struct {
	logger      *zap.Logger
	compressionThreshold int
	mu          sync.RWMutex
}

func NewManager(logger *zap.Logger) *Manager {
	return &Manager{
		logger:               logger,
		compressionThreshold: 256,
	}
}

func (m *Manager) SetCompressionThreshold(threshold int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.compressionThreshold = threshold
}

func (m *Manager) GetCompressionThreshold() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.compressionThreshold
}