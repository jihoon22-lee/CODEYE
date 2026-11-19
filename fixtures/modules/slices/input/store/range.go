package store

// Range is part of the store's range path.

type rangeState struct {
	keys  []string
	sizes map[string]int
	limit int
}

func newRangeState(limit int) *rangeState {
	return &rangeState{sizes: map[string]int{}, limit: limit}
}

// RangeStep0 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *rangeState) RangeStep0(key string, size int) int {
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

// RangeStep1 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *rangeState) RangeStep1(key string, size int) int {
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

// RangeStep2 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *rangeState) RangeStep2(key string, size int) int {
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

// RangeStep3 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *rangeState) RangeStep3(key string, size int) int {
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
