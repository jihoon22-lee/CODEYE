package store

// Cursor is part of the store's cursor path.

type cursorState struct {
	keys  []string
	sizes map[string]int
	limit int
}

func newCursorState(limit int) *cursorState {
	return &cursorState{sizes: map[string]int{}, limit: limit}
}

// CursorStep0 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *cursorState) CursorStep0(key string, size int) int {
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

// CursorStep1 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *cursorState) CursorStep1(key string, size int) int {
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

// CursorStep2 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *cursorState) CursorStep2(key string, size int) int {
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

// CursorStep3 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *cursorState) CursorStep3(key string, size int) int {
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
