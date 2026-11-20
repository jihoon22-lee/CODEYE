# JavaScript/TypeScript 검토 팩

## 먼저 확인

Node/browser target, ESM/CJS, TS strict 옵션, tsconfig·workspace 경계, framework lifecycle을 확인한다. 의존성이나 타입 선언이 없어 생긴 타입 진단은 코드 결함이 아니라 분석 공백이다. compile 성공이나 타입 선언을 런타임 입력 검증으로 보지 않는다.

## 핵심 검토

- Promise: return·await 누락, 처리되지 않는 rejection, async callback을 기다리지 않는 API, Promise.all 부분 실패.
- 순서/경쟁: 오래된 응답이 최신 상태를 덮기, stale closure, 재진입, 요청 사이에 공유되는 mutable state.
- 취소/정리: AbortSignal 전파, timer·listener·subscription 정리, timeout 뒤에도 계속되는 작업.
- 타입 경계: any·assertion·non-null assertion으로 가려진 외부 입력, JSON parse 결과, 빠진 union 분기.
- 입력 경계: 요청·메시지 입력을 검증하는 위치, undefined/null/빈 값, number 정밀도, prototype 경계.
- 보안: 입력이 HTML·URL·경로·shell·query sink에 도달하는지, 문맥에 맞는 escaping, 인증과 권한 확인의 구분.
- 성능: event loop blocking, 무제한 Promise fan-out, 큰 buffer와 반복 직렬화.

## 반증

framework cleanup, 상위 error handler, 입력 schema 검증 계층, 요청 version·idempotency 처리, compiler가 배제한 분기를 확인한다. await 없는 Promise가 모두 결함은 아니며 의도된 background 작업이면 오류 관찰과 수명을 확인한다.

## 제출

실행 target과 입력 출처, 비동기 실행 순서, 영향을 받는 상태, 확인한 방어와 실패 조건을 명시한다. 경쟁 문제는 가능한 이벤트 순서를 짧은 근거 중심 시나리오로 제시한다. 병렬성을 무조건 없애는 수정 대신 취소·오류 전파를 보존한다.
