# Python 검토 팩

## 먼저 확인

대상 interpreter 버전, package·venv 구성, import 해석, type checker 설정과 stub, framework lifecycle을 확인한다. interpreter·의존성·stub이 없어 import나 타입을 해석하지 못한 진단은 코드 결함이 아니라 분석 공백이다. 타입 annotation을 런타임 검증으로 보지 않는다.

## 핵심 검토

- 오류 흐름: broad except, 예외를 성공값·None으로 바꾸기, finally가 원래 예외를 덮기, 기록만 하고 계속 진행하기.
- 보안: 외부 입력이 shell·subprocess·파일 경로·SQL·템플릿·역직렬화·eval sink에 도달하는지와 방어의 맥락.
- 자원: 파일·연결·lock·임시 디렉터리·subprocess 정리가 성공·실패·취소 경로 모두에 있는가.
- 상태: mutable default 인자, 모듈·클래스 수준 공유 상태, iterator·generator 소모.
- 비동기: 기다리지 않는 coroutine·task, task 예외 관찰, cancellation 전파, event loop 안의 blocking 호출.
- 데이터 경계: None·빈 값·false 값 구분, 정수/실수 변환, timezone·단위, parse 결과와 annotation 차이.
- import/버전: 선택적 import의 fallback, 버전별 API·예외 차이, 순환 import의 초기화 순서.

## 반증

상위 context manager·finally, framework의 오류 처리, 입력 검증 계층, 호출자의 예외 처리 계약을 확인한다. annotation이 없다는 이유만으로 오류를 확정하지 않는다. sink 이름만 보고 취약점을 확정하지 말고 외부 입력이 실제로 도달하는 경로를 확인한다.

## 제출

함수와 호출자, 입력 출처와 sink, 오류·취소 조건, 남는 자원이나 잘못된 값을 연결한다. 해석하지 못한 import·타입에 기대는 주장은 그 공백을 적는다. 읽은 테스트와 실행한 테스트를 구분한다.
