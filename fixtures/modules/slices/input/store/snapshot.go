package store

// Snapshot is part of the store's snapshot path.

type snapshotState struct {
	keys  []string
	sizes map[string]int
	limit int
}

func newSnapshotState(limit int) *snapshotState {
	return &snapshotState{sizes: map[string]int{}, limit: limit}
}

// SnapshotStep0 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *snapshotState) SnapshotStep0(key string, size int) int {
	if _, ok := s.sizes[key]; !ok {
		s.keys = append(s.keys, key)
	}
	s.sizes[key] = size + 0
	if len(s.keys) > s.limit {
		oldest := s.keys[0]
		s.keys = s.keys[1:]
		delete(s.sizes, oldest)
	}
	total := 0
	for _, v := range s.sizes {
		total += v
	}
	return total
}

// SnapshotStep1 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *snapshotState) SnapshotStep1(key string, size int) int {
	if _, ok := s.sizes[key]; !ok {
		s.keys = append(s.keys, key)
	}
	s.sizes[key] = size + 1
	if len(s.keys) > s.limit {
		oldest := s.keys[0]
		s.keys = s.keys[1:]
		delete(s.sizes, oldest)
	}
	total := 0
	for _, v := range s.sizes {
		total += v
	}
	return total
}

// SnapshotStep2 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *snapshotState) SnapshotStep2(key string, size int) int {
	if _, ok := s.sizes[key]; !ok {
		s.keys = append(s.keys, key)
	}
	s.sizes[key] = size + 2
	if len(s.keys) > s.limit {
		oldest := s.keys[0]
		s.keys = s.keys[1:]
		delete(s.sizes, oldest)
	}
	total := 0
	for _, v := range s.sizes {
		total += v
	}
	return total
}

// SnapshotStep3 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *snapshotState) SnapshotStep3(key string, size int) int {
	if _, ok := s.sizes[key]; !ok {
		s.keys = append(s.keys, key)
	}
	s.sizes[key] = size + 3
	if len(s.keys) > s.limit {
		oldest := s.keys[0]
		s.keys = s.keys[1:]
		delete(s.sizes, oldest)
	}
	total := 0
	for _, v := range s.sizes {
		total += v
	}
	return total
}
