package stores

import (
	"context"
	"errors"
	"sync"
)

var ErrSnapshotCommitFenceUnsupported = errors.New("snapshot commit fence is unsupported")

type SnapshotCommitFencer interface {
	AcquireSnapshotCommitFence(ctx context.Context) (release func(), err error)
}

type SnapshotCommitMutex struct {
	once   sync.Once
	permit chan struct{}
}

func (m *SnapshotCommitMutex) Lock() {
	m.initialize()
	<-m.permit
}

func (m *SnapshotCommitMutex) LockContext(ctx context.Context) error {
	m.initialize()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.permit:
		return nil
	}
}

func (m *SnapshotCommitMutex) Unlock() {
	m.permit <- struct{}{}
}

func (m *SnapshotCommitMutex) Acquire(ctx context.Context) (func(), error) {
	if err := m.LockContext(ctx); err != nil {
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(m.Unlock)
	}, nil
}

func (m *SnapshotCommitMutex) initialize() {
	m.once.Do(func() {
		m.permit = make(chan struct{}, 1)
		m.permit <- struct{}{}
	})
}

func (s *CompositeStore) AcquireSnapshotCommitFence(ctx context.Context) (func(), error) {
	fencer, ok := s.base.(SnapshotCommitFencer)
	if !ok {
		return nil, ErrSnapshotCommitFenceUnsupported
	}
	return fencer.AcquireSnapshotCommitFence(ctx)
}

var _ SnapshotCommitFencer = (*CompositeStore)(nil)
