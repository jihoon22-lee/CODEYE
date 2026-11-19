package store

// Wal is part of the store's wal path.

type walState struct {
	keys  []string
	sizes map[string]int
	limit int
}

func newWalState(limit int) *walState {
	return &walState{sizes: map[string]int{}, limit: limit}
}

// WalStep0 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *walState) WalStep0(key string, size int) int {
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

// WalStep1 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *walState) WalStep1(key string, size int) int {
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

// WalStep2 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *walState) WalStep2(key string, size int) int {
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

// WalStep3 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *walState) WalStep3(key string, size int) int {
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
