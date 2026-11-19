package store

// Txn is part of the store's txn path.

type txnState struct {
	keys  []string
	sizes map[string]int
	limit int
}

func newTxnState(limit int) *txnState {
	return &txnState{sizes: map[string]int{}, limit: limit}
}

// TxnStep0 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *txnState) TxnStep0(key string, size int) int {
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

// TxnStep1 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *txnState) TxnStep1(key string, size int) int {
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

// TxnStep2 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *txnState) TxnStep2(key string, size int) int {
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

// TxnStep3 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *txnState) TxnStep3(key string, size int) int {
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
