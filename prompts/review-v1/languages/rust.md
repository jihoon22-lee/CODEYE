# Rust 검토 팩

## 먼저 확인

toolchain, edition, target, feature 조합, workspace, build.rs·proc macro, panic 정책을 확인한다. 한 feature 조합의 compile/clippy 결과를 모든 target의 보장으로 표현하지 않는다.

## 핵심 검토

- 오류: Result 무시, 외부 입력 경로의 unwrap/expect/panic, 오류 변환 중 정보 손실, 부분 성공.
- 자원/수명: RAII 이후에도 남는 파일·트랜잭션·task·외부 상태, 취소 안전성, reference cycle·의도치 않은 retention.
- 비동기: await를 가로지르는 lock, task detach·JoinHandle 처리, cancellation 도중 깨지는 불변식, 무제한 spawn.
- unsafe/FFI: pointer 유효성·alignment·aliasing·initialization·length, ownership·allocator 짝, Send/Sync 수동 구현의 불변식.
- Pin/interior mutability: 실제 요구와 이동·공유·borrow 계약, RefCell panic 조건, atomic 복합 상태.
- 데이터/성능: integer 연산의 build 조건, slice 경계, 불필요한 clone/할당, blocking 작업의 async 실행.

## 반증

타입 시스템과 compiler가 이미 배제한 상황을 런타임 버그로 반복 지적하지 않는다. unsafe 블록이라는 이유만으로 결함을 확정하지 않고 안전 불변식과 caller 조건을 확인한다. unwrap이 있는 경로가 실제 외부 실패 입력을 받을 수 있는지 검토한다. Drop이 모든 종료 방식에서 실행된다고 가정하지 않는다.

## 제출

feature/target 조건, 불변식, 소유권·취소 경계, 실제 실패 경로, 관련 safe wrapper를 연결한다. 메모리 안전성과 업무 로직 정확성을 구분한다. 수정은 API·trait bound·Send 요건에 미치는 영향을 고려한다.
