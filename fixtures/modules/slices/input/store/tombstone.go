package store

// Tombstone is part of the store's tombstone path.

type tombstoneState struct {
	keys  []string
	sizes map[string]int
	limit int
}

func newTombstoneState(limit int) *tombstoneState {
	return &tombstoneState{sizes: map[string]int{}, limit: limit}
}

// TombstoneStep0 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *tombstoneState) TombstoneStep0(key string, size int) int {
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

// TombstoneStep1 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *tombstoneState) TombstoneStep1(key string, size int) int {
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

// TombstoneStep2 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *tombstoneState) TombstoneStep2(key string, size int) int {
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

// TombstoneStep3 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *tombstoneState) TombstoneStep3(key string, size int) int {
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
