# CODEYE 문서 인덱스

설계 기준선 v0.1. W01~W06의 구현 범위와 검증 환경은 아래 개발 기록에 정리했다. W07은 워커 자동 증감, W08은 첫 실도구 분석기 단계다. 성능 목표와 초기 운영값은 각 명세에서 확인할 수 있다.

## 제품과 핵심 설계

| 문서 | 내용 |
|---|---|
| [00 제품 개요와 범위](00-product-and-scope.md) | 확정 요구, 비목표, 단계별 제공 범위 |
| [01 요구사항](01-requirements.md) | 추적 가능한 기능·비기능 요구사항 |
| [02 분석 품질 원칙](02-analysis-quality.md) | 깊은 검토, 근거, 반증, 분석 범위, 보고서 품질 |
| [03 아키텍처](03-architecture.md) | 관리 서버·노드·워커·저장소·모델의 경계 |
| [04 분석 파이프라인](04-analysis-pipeline.md) | 분석 DAG, 상태 전이, 부분 실패, 캐시 |
| [05 소스와 실행 환경](05-source-and-environments.md) | Git/local 스냅샷, NFS, 환경 탐색·재현 |
| [06 도구 어댑터](06-analyzer-contract.md) | 분석기 계약, 언어별 도구 구성, 정규화 |
| [07 LLM과 분석 지침](07-llm-and-instructions.md) | 검토 역할, 컨텍스트, OpenCode, 지침 버전 |
| [08 Native 실행](08-native-execution.md) | Windows/Linux 프로세스·자원·취소·신뢰 경계 |
| [09 워커·예약·자동 증감](09-workers-and-scheduling.md) | 다중 노드, 최소·최대, 공정성, lease·복구 |

## 계약과 사용자 경험

| 문서 | 내용 |
|---|---|
| [10 데이터 모델](10-data-model.md) | 엔티티, 무결성, 발견 사항·증거·이력 |
| [11 API와 이벤트](11-api-and-events.md) | 인증, 멱등성, API 계약, SSE, 오류 |
| [12 보고서와 UI](12-report-and-ui.md) | 요약·영역·상세, 진행 표시, 접근성·내보내기 |
| [13 GitHub·GHES](13-github-and-ghes.md) | App, 저장소 연결, 웹훅, PR·댓글 명령 |
| [14 보안·권한](14-security-and-governance.md) | 프로젝트 권한, Native 한계, 프롬프트 주입, 보관 |
| [15 배포·운영](15-deployment-and-operations.md) | 개인 PC, 서버, 폐쇄망, 백업·장애 대응 |

## 개발 실행

| 문서 | 내용 |
|---|---|
| [16 분석 품질 평가](16-evaluation.md) | 데이터셋, precision/recall 범위, 근거·보고서 평가 |
| [17 구현 작업 계획](17-implementation-plan.md) | 작업 ID, 선행 관계, 산출물, 완료 조건 |
| [18 수용 테스트·추적표](18-acceptance-and-traceability.md) | 성공·실패·장애·보안·품질의 검증 시나리오 |
| [19 결정과 미확정 사항](19-decisions-and-open-questions.md) | 설계 기본값, 검증할 가정, 변경 절차 |
| [20 설계 일관성 점검](20-design-review.md) | 충돌하기 쉬운 계약과 구현 검토 질문 |
| [21 W01 실행 기반](21-w01-foundation.md) | 실제 실행·개발 환경·doctor 계약·테스트와 남은 제한 |
| [22 W02 착수 준비](22-w02-preparation.md) | 도메인·DB 범위, 트랜잭션 시험과 착수 전 결정 항목 |
| [23 W02 저장 계약](23-w02-storage.md) | 도메인·migration·ACL·lease·근거 metadata와 실제 DB 시험 |
| [24 W03 API·인증](24-w03-api.md) | loopback 세션·프로젝트/run·브라우저 통합 검증 |
| [25 W04 소스 확보](25-w04-source.md) | Git/local 고정·manifest·독립 workspace·경계 시험 |
| [26 W05 Native 실행기](26-w05-native.md) | argv·env·프로세스 트리·timeout·종료 확인 |
| [27 W06 착수 준비](27-w06-preparation.md) | 원계획 대조, 프로토콜 통합 쟁점과 두 에이전트 수용 시험 |
| [28 W06 노드 API](28-w06-node-protocol.md) · [에이전트 실행](28-w06-agent-runtime.md) | 등록·배정·lease·취소·복구 및 실프로세스 검증 |
| [29 W08 Go vet](29-w08-go-vet.md) | 실제 Go 도구, 원시 관찰과 미분석 범위 |
| [32 W10 OpenCode](32-w10-opencode.md) | 응답·취소·도구 권한 차단과 호출 예산 |
| [33 W11 맥락 검토](33-w11-context-review.md) | 소스 승인, 발췌와 근거 ID, 실제 역할 실행 |
| [34 W12 후보 검증](34-w12-verifier.md) | 호출자·테스트 반례, 근거 검증과 부분 완료 |

## 기계 판독 계약과 예시

- [상태 Schema](../schemas/state.schema.json), [상태 예시](../examples/state.json): W02 상태 어휘와 API 일치 검증.
- [Doctor Schema](../schemas/doctor.schema.json), [합성 진단 예시](../examples/doctor.json): W01 CLI 결과 계약.

- [발견 사항 Schema](../schemas/finding.schema.json), [계획 Schema](../schemas/plan.schema.json), [보고서 Schema](../schemas/report.schema.json), [도구 Schema](../schemas/analyzer.schema.json).
- [핵심 OpenAPI 계약](../api/openapi.yaml): 핵심 수직 경로를 정의한다. 모든 운영 CRUD가 완성된 명세라는 뜻은 아니다.
- [플랫폼 설정](../examples/platform.yaml), [프로젝트 설정](../examples/project.yaml), [Ruff 어댑터 예시](../examples/analyzer-ruff.yaml), [계획 예시](../examples/plan.json).
- [합성 보고서](../examples/report.md), [기계 판독 보고서](../examples/report.json), [합성 코드와 평가 정답](../fixtures/cleanup/README.md).
- [프롬프트 팩 기본 지침](../prompts/review-v1/00-core.md), [팩 manifest](../prompts/review-v1/pack.yaml).

기술 검토자는 02→04→07→10→12→16, 실행기 구현자는 03→05→08→09→11→18, 제품 구현자는 00→01→12→17 순서로 읽는 것을 권장한다.

- [Snapshot manifest Schema](../schemas/snapshot-manifest.schema.json), [local 예시](../examples/snapshot-manifest.json), [Git 예시](../examples/snapshot-manifest-git.json).
