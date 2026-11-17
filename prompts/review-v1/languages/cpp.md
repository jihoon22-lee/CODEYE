# C/C++ 검토 팩

## 먼저 확인

언어 표준, compiler/target/ABI, defines, include 경로, compile DB 범위, exception/RTTI 옵션, 생성 헤더를 읽는다. 특정 macro/config에서만 성립하는 주장은 그 조건을 적는다. 파싱하지 못한 translation unit을 확인된 영역으로 표시하지 않는다.

## 핵심 검토

- 객체 수명: 지역 객체 주소/참조, temporary, string_view/span/iterator, container 재할당 뒤 참조 유효성.
- 소유권: raw pointer의 실제 owner, unique/shared 소유 이전, move 후 사용, cycle, 수동 free와 RAII 중복.
- 예외: 획득 중간 실패, destructor의 예외, noexcept 경로, 부분 초기화, 강한/기본 예외 보장 계약.
- 메모리/정수: bounds, signed overflow, narrowing, 길이 계산, alignment, aliasing, uninitialized 값. 도달 가능한 값 범위 확인.
- 동시성: 데이터 race, lock 순서, atomic memory order와 실제 불변식, callback 재진입, 객체 파괴 중 다른 thread.
- 파일/프로세스: handle 정리, partial I/O, EINTR류 처리의 환경 조건, 경로 검증, subprocess 인자.
- 성능: hot path 복사·할당·중첩 탐색·lock contention. 실제 workload 없이 시간 수치 주장 금지.

## 반증

호출자 lifetime 보장, static storage, 소유권 문서, 상위 mutex, non-throwing 계약, allocator/arena 정리, compile-time 제외를 확인한다. raw pointer나 manual memory라는 이유만으로 결함을 만들지 않는다. 도구의 경로가 실제 build flags와 일치하는지 확인한다.

## 제출

문제의 build variant, source 경로, 수명/제어 흐름, 발생 조건, 영향, 확인한 방어, 필요한 최소 회귀 테스트를 적는다. 수정 제안은 ABI·호출 규약·예외 정책과 충돌할 수 있는지 설명한다.
