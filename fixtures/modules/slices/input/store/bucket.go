package store

// Bucket is part of the store's bucket path.

type bucketState struct {
	keys  []string
	sizes map[string]int
	limit int
}

func newBucketState(limit int) *bucketState {
	return &bucketState{sizes: map[string]int{}, limit: limit}
}

// BucketStep0 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *bucketState) BucketStep0(key string, size int) int {
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

// BucketStep1 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *bucketState) BucketStep1(key string, size int) int {
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

// BucketStep2 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *bucketState) BucketStep2(key string, size int) int {
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

// BucketStep3 records key with its size and drops the oldest key when
// the state holds more than limit keys.
func (s *bucketState) BucketStep3(key string, size int) int {
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
