# 20. 설계 일관성 검토 체크리스트

## 1. 제품 경계

분석 결과 수집을 기존 CI로 돌리지 않았는가. Native 실행을 기본 설치에서 실제로 지원하는가. 문서 없는 프로젝트와 로컬 경로를 PR 저장소보다 열등한 부가 기능으로 만들지 않았는가. 의존성 CVE가 기본 단계나 실패 분모에 숨어 있지 않은가.

## 2. 입력과 환경

source snapshot과 실행 workspace가 구분되는가. Git 최신 기준이 acquire인지 명확한가. 재시도에서 브랜치가 새로 바뀌지 않는가. 로컬 복사 일관성의 한계를 표시하는가. 생성 파일·header·submodule·LFS·경로 재매핑을 고려하는가. 프로젝트 runtime과 검사기 runtime을 구분하는가.

## 3. 실행과 자원

워커는 별도 머신과 별도 프로세스 개념으로 구분되는가. min0에서도 재기동 주체가 살아 있는가. max 축소가 busy kill로 구현되지 않았는가. process admission과 hard limit를 혼동하지 않는가. 내부 build 병렬도와 LLM 동시성까지 예산에 포함하는가. 프로세스 종료 확인 없이 취소 완료를 표시하지 않는가.

## 4. 분산 상태

claim과 capacity reservation이 원자적인가. command/event/result가 멱등적인가. epoch 검사가 artifact finalize까지 적용되는가. controller/agent 재시작과 partition에서 상태를 복원하는가. 이전 프로세스의 부작용 가능성이 남았는데 자동 재시도하지 않는가. DB lock을 프로세스 실행 시간 동안 잡고 있지 않은가.

## 5. 분석 품질

맥락 중심 탐색을 도구 경고 해석으로 대체하지 않았는가. 문서가 없을 때 업무 규칙을 만들어내지 않는가. 반증·상위 방어·소유권 이전·버전 조건을 확인하는가. 여러 모델의 동의를 재현으로 표현하지 않는가. 과도한 보수성으로 모든 후보를 숨겨 recall이 무너지는지 평가하는가.

## 6. 근거와 보고서

위치·hash·실행 artifact를 서버가 확인하는가. 원시 경고와 통합 finding이 분리되는가. severity와 validation과 disposition이 분리되는가. missing 검사 때문에 사라진 문제를 fixed로 처리하지 않는가. 한계·분석 공백이 요약에 보이는가. export와 UI의 숫자가 같은 데이터에서 나오는가.

## 7. 진행·UI

브라우저 종료가 작업을 종료하지 않는가. SSE gap과 권한 철회를 처리하는가. task 추가로 분모가 바뀔 때 revision을 보여주는가. LLM 토큰 수나 연결 heartbeat로 진행률을 꾸미지 않는가. 첫 실행에서 근거 없는 ETA를 제공하지 않는가. 현재 run과 마지막 보고서를 혼동하지 않는가.

## 8. 보안과 운영

서비스 OS 권한을 웹 사용자에게 그대로 위임하지 않는가. 저장소 지침이 secret·MCP·shell 권한을 바꾸지 못하는가. loopback 모드에도 웹 Origin/CSRF 경계가 있는가. raw logs/export에 코드와 비밀값이 새지 않는가. 내부 CA/미러 오류를 인터넷 우회나 TLS 검증 해제로 해결하지 않는가. 복원된 lease가 살아나 중복 실행하지 않는가.

## 9. 개발 완료 판정

문서 링크와 schema 검증, 코드 unit/integration, Native E2E, 분산 장애, 분석 품질, 보고서 이해도는 서로 다른 검증이다. 어떤 검증을 실행했는지 정확히 보고하는가. mock UI나 합성 fixture 테스트만 통과하고 전체 CODEYE 구현 완료라고 쓰지 않는가.
