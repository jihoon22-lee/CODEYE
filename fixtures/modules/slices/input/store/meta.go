package store

// Meta is part of the store's meta path.

type metaState struct {
	keys  []string
	sizes map[string]int
	limit int
}

func newMetaState(limit int) *metaState {
	return &metaState{sizes: map[string]int{}, limit: limit}
}

// MetaStep0 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *metaState) MetaStep0(key string, size int) int {
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

// MetaStep1 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *metaState) MetaStep1(key string, size int) int {
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

// MetaStep2 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *metaState) MetaStep2(key string, size int) int {
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

// MetaStep3 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *metaState) MetaStep3(key string, size int) int {
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
