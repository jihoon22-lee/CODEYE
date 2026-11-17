# Python 검토 팩

## 먼저 확인

대상 interpreter 버전, package/venv, type checker 설정·stub·import 해석, framework lifecycle, async 사용을 확인한다. 타입 annotation과 실제 런타임 검증을 동일시하지 않는다.

## 핵심 검토

- 오류 경로: broad except, 예외를 성공값으로 바꾸는 코드, finally에서 원래 예외 덮기, 부분 write.
- 자원: 파일·임시 디렉터리·연결·lock·subprocess 정리가 성공/실패/취소 모두에 있는가.
- 상태: mutable default, 공유 cache, 클래스/인스턴스 속성 혼동, iterator 소모, aliasing.
- 비동기: 생성했지만 기다리지 않는 coroutine/task, task 예외 관찰, cancellation 전파, event loop의 blocking I/O.
- 데이터 경계: None/빈 값/false 값 구분, parse 결과와 annotation 차이, timezone/단위, 입력 size.
- 동시 실행: thread/process/async에서 실제 공유되는 상태, 파일 잠금과 원자 rename, retry 멱등성.
- 보안: 사용자 입력이 shell·파일 경로·템플릿·역직렬화 경로에 도달하는지와 방어 맥락.

## 반증

상위 context manager/finally, callback 계약, framework teardown, 테스트 fixture의 의도, version별 예외 계층을 확인한다. annotation이 없다는 이유로 오류를 확정하지 않는다. 사용자가 소유한 generator/iterator의 정리 책임을 무조건 피호출자에게 부여하지 않는다.

## 제출

함수와 호출자, 오류/취소 조건, 남는 자원이나 잘못된 값, 실제 정리 경로를 연결한다. 기존 테스트를 읽은 것과 실행한 것을 분리한다. 수정은 public API·예외 타입·backward compatibility를 고려한다.
