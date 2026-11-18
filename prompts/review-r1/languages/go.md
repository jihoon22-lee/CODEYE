# Go 검토 팩

## 먼저 확인

Go version/toolchain, modules/workspace, GOOS/GOARCH, build tags, CGO, 외부 패키지 계약을 확인한다. build에서 제외된 파일과 현재 구성에 포함된 파일을 구분한다.

## 핵심 검토

- context: deadline/cancel 전파, CancelFunc 호출, 요청 종료 후 계속되는 goroutine·I/O.
- goroutine/channel: 모든 종료 경로, blocked send/receive, close 소유자, double close, 무제한 fan-out.
- 오류: 무시/덮어쓰기, wrapping과 caller 처리 계약, typed nil interface, 성공값과 오류 불일치.
- 자원: response.Body·file·rows·transaction 정리, loop 안의 defer 누적, commit/rollback 경로.
- 공유 상태: map/slice·pointer 접근, mutex copy·lock 순서, atomic과 복합 불변식.
- 데이터: integer conversion, zero value 의미, time.Duration 단위, partial read/write, scanner 제한.
- 테스트/성능: race 가능 경로, timeout 없는 외부 호출, queue/buffer 증가, hot path 할당.

## 반증

외부 owner가 channel을 닫는 계약, caller defer, 제한된 worker pool, 상위 context, 안전한 zero value 설계를 확인한다. defer가 있다는 이유만으로 언제 실행되는지 확인하지 않고 안전하다고 하지 않는다. context 사용 여부만으로 실제 취소되는지 추정하지 않는다.

## 제출

goroutine·channel·자원 owner와 종료 조건을 명시한다. 동시성 문제는 가능한 실행 순서를 짧은 근거 중심 시나리오로 제시한다. race detector를 실행하지 않았다면 검출됐다고 쓰지 않는다.
