# JavaScript/TypeScript 검토 팩

## 먼저 확인

Node/browser target, TS 버전·strict 옵션, tsconfig references, module ESM/CJS, workspace 경계, framework lifecycle을 확인한다. compile 성공을 런타임 입력 검증으로 해석하지 않는다.

## 핵심 검토

- Promise: return/await 누락, rejection 누락, async callback 계약, Promise.all 부분 실패와 이미 시작한 작업.
- 상태·경쟁: stale closure, 오래된 응답의 최신 상태 덮기, 재진입, 여러 요청의 공유 mutable state.
- 취소: AbortSignal 전파, timer/listener/subscription/socket 정리, timeout 후 계속 실행되는 작업.
- 타입 경계: any/assertion/non-null assertion으로 가려진 실제 입력, JSON parse, discriminated union 누락.
- 데이터: undefined/null/빈 값, number 정밀도, 문자열 인코딩, 날짜·timezone, object key 경계.
- 보안: 입력→HTML/URL/경로/shell/query의 실제 sink, 문맥에 맞는 escaping, 인증과 자원 권한 구분.
- 성능: event loop blocking, 무제한 Promise fan-out, 큰 버퍼·반복 직렬화. 추정 복잡도와 실측 구분.

## 반증

프레임워크 cleanup, 상위 error boundary와 서버 error handler, idempotency key, monotonic request version, compiler가 실제 배제한 분기를 확인한다. 모든 await 없는 Promise가 버그는 아니며 의도된 background 작업의 오류 관찰·수명을 확인한다. CSS/formatting 취향은 동작 결함과 구분한다.

## 제출

실제 실행 타깃과 입력, 비동기 시간 순서, 영향을 받는 상태, 방어 코드와 실패 조건을 명시한다. 해결책으로 무조건 동기화하거나 병렬성을 없애지 말고 계약과 취소·오류 전파를 보존한다.
