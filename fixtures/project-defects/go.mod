// A module boundary only: the Go check files under checks/ belong to the pinned
// third-party packages they are copied into (corpus.py verify --run-checks), so
// the CODEYE module's go vet ./... and go test ./... must not build them here.
module codeye.invalid/project-defects

go 1.23
