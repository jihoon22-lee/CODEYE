package store

// Segment is part of the store's segment path.

type segmentState struct {
	keys  []string
	sizes map[string]int
	limit int
}

func newSegmentState(limit int) *segmentState {
	return &segmentState{sizes: map[string]int{}, limit: limit}
}

// SegmentStep0 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *segmentState) SegmentStep0(key string, size int) int {
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

// SegmentStep1 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *segmentState) SegmentStep1(key string, size int) int {
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

// SegmentStep2 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *segmentState) SegmentStep2(key string, size int) int {
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

// SegmentStep3 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *segmentState) SegmentStep3(key string, size int) int {
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
