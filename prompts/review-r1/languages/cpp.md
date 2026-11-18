# C/C++ 검토 팩

## 먼저 확인

언어 표준, compiler·target, defines, include 경로, compile DB가 덮는 translation unit, 생성 헤더를 확인한다. 파싱하지 못했거나 compile DB에 없는 translation unit은 확인된 영역이 아니다. 헤더·의존성이 없어 생긴 진단은 코드 결함이 아니라 분석 공백이다.

## 핵심 검토

- 수명: 지역 객체의 주소·참조 반환, temporary, string_view·span·iterator, container 재할당 뒤 남은 참조.
- 소유권: raw pointer의 실제 owner, unique/shared 소유 이전, move 후 사용, double free, 수동 해제와 RAII 중복.
- UB: 범위 밖 접근, signed overflow, 초기화되지 않은 값, null 역참조, alignment·aliasing.
- 정수/길이: narrowing, signed/unsigned 비교, 크기 계산 overflow, buffer 크기와 종료 문자.
- 오류/예외: 획득 중간 실패, destructor의 예외, noexcept 경로, 무시된 반환 코드, 부분 초기화.
- 동시성: data race, lock 순서, atomic memory order와 불변식, 파괴 중인 객체의 다른 thread 접근.
- 자원/성능: file·socket·handle 정리, partial I/O, hot path 복사·할당.

## 반증

호출자의 lifetime 보장, static storage, 소유권 문서, 상위 mutex, non-throwing 계약, arena 정리, 전처리로 제외된 코드를 확인한다. raw pointer나 manual memory라는 이유만으로 결함을 만들지 않는다. UB 주장은 그 값이나 순서에 실제로 도달하는 경로를 확인한다.

## 제출

build 조건(standard·define·target), 수명·소유권 흐름, 발생 조건, 영향, 확인한 방어를 적는다. 분석하지 않은 translation unit에 기대는 주장은 그 공백을 적는다. sanitizer를 실행하지 않았다면 검출됐다고 쓰지 않는다.
