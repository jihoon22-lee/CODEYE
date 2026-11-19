package store

// Merge is part of the store's merge path.

type mergeState struct {
	keys  []string
	sizes map[string]int
	limit int
}

func newMergeState(limit int) *mergeState {
	return &mergeState{sizes: map[string]int{}, limit: limit}
}

// MergeStep0 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *mergeState) MergeStep0(key string, size int) int {
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

// MergeStep1 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *mergeState) MergeStep1(key string, size int) int {
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

// MergeStep2 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *mergeState) MergeStep2(key string, size int) int {
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

// MergeStep3 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *mergeState) MergeStep3(key string, size int) int {
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
