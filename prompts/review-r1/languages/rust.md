# Rust 검토 팩

## 먼저 확인

toolchain, edition, target, feature 조합, workspace, build.rs·proc macro, panic 정책을 확인한다. 도구 관찰은 분석한 feature·target 조합 하나의 결과다. 의존성이나 실행 허가가 없어 검사를 끝내지 못한 경우는 코드 결함이 아니라 분석 공백이다.

## 핵심 검토

- unsafe: 블록이 기대는 불변식(pointer 유효성·alignment·aliasing·초기화·길이), 이를 보장하는 caller 조건, 안전 설명.
- FFI/수동 구현: ownership·allocator 짝, lifetime 연장, Send/Sync 수동 구현, safe wrapper가 불변식을 강제하는가.
- panic: 외부 입력 경로의 unwrap·expect·indexing·slicing, overflow의 build 조건, Result를 돌려주는 함수 안의 panic, 남은 todo.
- 오류: Result 무시, 오류 변환 중 정보 손실, 부분 성공, panic과 오류 반환 계약의 혼동.
- 자원/취소: Drop에 기대는 정리, 취소된 future가 남기는 불변식, reference cycle.
- 비동기/공유: await를 가로지르는 lock, detach된 task, 무제한 spawn, RefCell borrow panic, atomic 복합 상태.

## 반증

타입 시스템과 borrow checker가 배제한 상황을 런타임 결함으로 지적하지 않는다. unsafe라는 이유만으로 결함을 확정하지 않고 불변식과 caller 조건을 확인한다. unwrap 경로가 실제 외부 실패 입력을 받는지 확인한다. Drop이 모든 종료 방식에서 실행된다고 가정하지 않는다.

## 제출

feature·target 조건, 깨질 수 있는 불변식, 소유권·취소 경계, panic이나 실패에 도달하는 입력, 관련 safe wrapper를 연결한다. 메모리 안전성과 업무 로직 정확성을 구분한다. Miri나 sanitizer를 실행하지 않았다면 검출됐다고 쓰지 않는다.
