package store

// Encode is part of the store's encode path.

type encodeState struct {
	keys  []string
	sizes map[string]int
	limit int
}

func newEncodeState(limit int) *encodeState {
	return &encodeState{sizes: map[string]int{}, limit: limit}
}

// EncodeStep0 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *encodeState) EncodeStep0(key string, size int) int {
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

// EncodeStep1 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *encodeState) EncodeStep1(key string, size int) int {
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

// EncodeStep2 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *encodeState) EncodeStep2(key string, size int) int {
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

// EncodeStep3 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *encodeState) EncodeStep3(key string, size int) int {
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
