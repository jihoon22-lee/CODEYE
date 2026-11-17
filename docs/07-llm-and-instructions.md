# 07. LLM 검토 엔진과 분석 지침

## 1. 책임 분리

LLM은 프로젝트 이해·탐색·가설·반증·설명을 담당한다. 실행 권한, 자원, 시간, 상태, 통계, 증거 존재 여부는 플랫폼이 결정한다. LLM 실패에도 도구 결과와 실행 범위를 보고할 수 있어야 한다. 모델이 없어 tool-only로 끝난 실행은 AI 심층 검토 완료로 표시하지 않는다.

## 2. 검토 역할

| 역할 | 입력 | 출력 |
|---|---|---|
| Context | manifest, 문서·설정, 주요 진입점 | 사실/추론 분리한 구조·계약·미확인 목록 |
| Investigator | 맥락, 언어·영역 지침, 소스 검색 | 후보, 발생 조건, 필요한 추가 근거 |
| Tool interpreter | 원시 진단과 주변 코드 | 진단 해석, 영향 경로, 중복 후보 |
| Verifier | 후보와 관련 코드/실행 근거 | supported/refuted/inconclusive/reproduced, 반증·미확인 |
| Synthesizer | 정규화된 관찰·검토·scope | root-cause 그룹, 우선순위·후속 검증 |
| Reporter | 검증된 finding/report JSON | 요약·영역별 설명·카드, evidence ID 참조 |

여러 역할은 하나의 모델로도 실행할 수 있다. 다중 에이전트의 수나 모델 간 동의 자체를 정확도 근거로 쓰지 않는다. 핵심은 단계별 입력·출력·검증과 예산이다.

## 3. 지침 계층

1. 플랫폼 불변 정책: 권한·비밀정보·입력 고정·실행 경계.
2. CODEYE core: 사실/추론/재현 구분, 근거 규칙, 출력 계약.
3. 언어·영역 팩: C++/Python/JS·TS/Go/Rust, 동시성·오류·보안·구조 등.
4. 운영자/프로젝트 관리자 지침: 승인된 범위의 관심 영역과 규칙.
5. 저장소 문서: 도메인·설계·코딩 관례의 분석 자료.

저장소의 AGENTS·README는 LLM의 실행 권한을 올리는 system instruction이 아니다. 하위 디렉터리 문서는 해당 경로의 맥락으로 연결한다. 상충하는 문서·설정·코드는 어느 쪽을 우선했다고 단정하지 말고 근거와 충돌을 기록한다. 문서가 없으면 소스 기반 추정으로 계속한다.

팩 manifest는 version·파일 목록·각 digest·합성 순서·schema version을 갖는다. 실제 run은 계산된 pack digest와 적용된 파일 목록을 저장한다. 프로젝트 정책과 저장소 문서의 digest도 기록한다.

## 4. 컨텍스트 구성

파일 목록→모듈/진입점→문서·환경 요약→심볼·참조 검색→후보에 필요한 호출부 순으로 확장한다. 필수 벡터 DB나 전체 소스 단일 프롬프트에 의존하지 않는다. 검색·AST·호출 그래프는 해결 범위와 신뢰 수준을 표시한다. 동적 호출·reflection·macro 때문에 불완전한 그래프를 완전한 흐름 증거로 취급하지 않는다.

컨텍스트 단위는 path, blob hash, range, symbol, origin, trust, relevance reason을 가진다. 원본을 다시 찾을 수 있는 요약만 캐시한다. 토큰 초과 시 무작위 절단하지 않고 중요 경로와 근거를 우선하며 제외를 ledger에 남긴다.

`context_exposed`와 `review_task_completed`를 기록하되 모델이 내용을 완전히 이해했다는 증명으로 쓰지 않는다. 접근 로그를 얻지 못하는 외부 OpenCode는 coverage_observability=limited다.

## 5. 모델 어댑터

```text
probe() -> identity, version, context_limit, capabilities
open_workspace(snapshot_id, workspace, policy) -> handle
submit(handle, role, context, output_contract, budget) -> request_id
events(request_id, cursor) -> status/tool-call/output events
cancel(request_id) -> cancellation_state
collect(request_id) -> structured_output + usage + artifacts
close(handle) -> cleanup_state
```

직접 API 어댑터는 허용된 endpoint에서만 사용한다. OpenAI 호환이라는 이름만으로 tool call·schema output·cancel을 지원한다고 가정하지 않는다. 지원하지 않는 기능은 capability에 표시하고 어댑터가 제한된 변환을 제공한다.

## 6. OpenCode 연결

기본은 노드 에이전트가 관리하는 QA 전용 OpenCode 실행과 작업별 workspace다. 기존 serve에 연결하는 옵션은 버전·API 계약·인증·경로 매핑·취소를 사전 진단한다. V1/V2 또는 사내 변형을 공개 최신 API와 동일하다고 가정하지 않는다. live OpenAPI 또는 명시한 버전 프로파일을 고정하고 계약 시험을 저장한다.

원격 serve가 워커 경로를 볼 수 있는지 확인한다. 단순히 같은 문자열의 경로를 보냈다는 이유로 동일 snapshot을 읽는다고 간주하지 않는다. workspace marker와 파일 digest를 비교한다. 접근 불가면 workspace_unreachable로 보고하며 개인 개발 디렉터리로 조용히 대체하지 않는다.

관리된 설정·별도 home/config·승인된 MCP/도구만 사용한다. 외부 검색·공유·업데이트·플러그인 설치는 폐쇄망 정책에 따라 끈다. 모델 서비스 credential만 필요한 경계에 전달하며 Git 게시 토큰은 넘기지 않는다. 세션 분리는 OS 파일/프로세스 격리가 아니다.

가능한 shell 작업은 플랫폼 task API로 실행한다. OpenCode 자체 shell이 필요한 배포는 승인된 allowlist와 Native 신뢰 모델을 명시하고 실행 이벤트를 기록한다. 자동 예약 중 권한 ask가 발생하면 policy_blocked로 제한시간 안에 끝내며 무기한 대기시키지 않는다.

## 7. 구조화 응답 검증

JSON Schema, enum, 필수 발생 조건, path/range, evidence ID, snapshot digest, 재현 근거를 기계적으로 검사한다. 지원 근거가 없는 강한 문장은 보류한다. 형식 오류는 오류 목록만 전달해 1회 복구하고, 의미나 증거 오류는 검토 task로 되돌리거나 inconclusive로 표시한다.

모델의 내부 추론 전문을 저장·표시하도록 요구하지 않는다. 대신 관찰 사실, 검토한 경로, 반증, 결론의 조건, 다음 확인 절차를 제출하게 한다. 보고서가 임의 source line이나 테스트 출력을 만들어낼 수 없도록 수집 계층이 증거 ID를 발급한다.

## 8. 예산·실패

run/task별 입력·출력 토큰, 호출 수, wall time, tool calls, 최대 검토 후보 수를 제한한다. provider가 usage를 반환하지 않으면 unknown이며 임의 비용을 계산하지 않는다. endpoint별 동시 요청 semaphore와 backoff를 공유한다. 프롬프트 길이와 비용·시간의 관계는 실제 모델 측정으로 조정한다.

모델 실패, context 초과, cancel 미지원, permission block은 개별 reason으로 보고한다. 단순히 재시도를 늘리는 것으로 분석을 끝내려 하지 않는다. 중요한 확인이 남았으면 미확정 항목과 다음 행동을 남기고 종료한다.

## 9. 품질 변경 관리

팩·모델·규칙 변경은 평가 정답을 숨긴 holdout에서 비교한다. 출력 수 증가보다 actionable precision, 중요한 결함 recall, evidence 정확성, 반증 철회율, 설명의 이해도를 본다. 미검증 모델 업그레이드를 모든 프로젝트의 기본값으로 자동 적용하지 않는다.
