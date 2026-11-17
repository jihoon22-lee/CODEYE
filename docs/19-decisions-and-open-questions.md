# 19. 결정 기록과 미확정 사항

## 1. 확정된 제품 결정

| ID | 결정 | 이유/결과 |
|---|---|---|
| D01 | 이름 CODEYE | 사용자 선택. 이름 탐색을 다시 열지 않음 |
| D02 | 독립 분석·검토·보고서 | 기존 CI 연결/수집은 범위 밖 |
| D03 | Native 기본, Container 선택 | 개인 PC·폐쇄망에서 컨테이너 없이 사용 |
| D04 | 소스·문서 자동 파악 | 설명·첨부를 강제하지 않아 레거시도 사용 |
| D05 | 초기 CVE 제외 | 패키지 선언은 환경 파악에만 사용 |
| D06 | 단일 머신→여러 노드 | 초기부터 API agent·lease·capacity 계약 |
| D07 | 최소/최대 자동 증감 | 단순 병렬 개수 설정만으로 대체하지 않음 |
| D08 | Git 브랜치 최신/로컬 복사 | 분석 입력 고정, 재시도와 새로 분석 구분 |
| D09 | 플랫폼 SSH 없음 | 운영자가 서비스를 띄우고 API로 연결 |
| D10 | 사내 OpenCode 우선 | 허용되지 않은 직접 API나 외부 모델로 우회 금지 |
| D11 | 별도 벤치마킹 문서 없음 | 조사 결과는 CODEYE 요구·설계·계약에 통합 |

## 2. 설계 기본안

| ID | 선택 | 대안과 조정 조건 |
|---|---|---|
| A01 | Go control/agent/worker | OS 프로세스 제어와 API 계약 공유. 환경 검증에서 큰 제약이 나오면 재검토 |
| A02 | React·TS 정적 UI | 복잡한 보고서·상태 탐색 요구. Node 서버는 운영 필수 아님 |
| A03 | PostgreSQL 단일 backend | 개인 설치 비용을 감수하고 분산 상태의 일관성 확보. 이중 DB 구현은 초기 제외 |
| A04 | 한 attempt는 한 node | 복구·workspace 일관성 단순화. 여러 run 분산만 우선 |
| A05 | local artifact store+API | NFS가 없어도 노드 확장. 대형 운영에서 object store 추가 |
| A06 | 벡터 DB 비필수 | 심볼·경로·문서 검색으로 시작, 실제 컨텍스트 품질 부족을 측정한 뒤 확장 |
| A07 | 검토 역할 분리 | 후보와 반증을 분리하되 다중 모델 필수화 안 함 |
| A08 | 단일 점수 미제공 | 발견·근거·분석 공백을 지표로 제공 |
| A09 | 기본 report JSON/MD/HTML | PDF 렌더링은 후속, 외부 자산 없는 export |
| A10 | OS능력 기반 자원 enforcement | hard limit와 admission을 구분, 컨테이너 요구로 우회하지 않음 |

이 기본안은 구현 시 합리적 근거와 계약/테스트 수정으로 변경할 수 있다. 사용자가 확정한 제품 요구를 기술 편의 때문에 바꾸지 않는다.

## 3. 확인할 환경 정보

| 항목 | 확인 방법 | 설계 영향 |
|---|---|---|
| 개인 PC OS/arch/RAM/local disk | doctor, 사용자 환경 확인 | 첫 배포 타깃·resource budget |
| 사내 cgroup·서비스 권한·로컬 디스크 | 실제 머신 probe | Native enforcement·상주·cache 위치 |
| OpenCode 버전/API/모델 제한 | live contract·workspace/cancel 시험 | adapter profile·동시 호출 상한 |
| GHES 버전/App 권한 | 연결 진단 | endpoint·webhook·댓글 지원 |
| toolchain·분석기 mirror | 설치·version·fixture 시험 | language capability matrix |
| 실제 개인 프로젝트 구성 | 권한 있는 snapshot 탐색 | 첫 언어와 R0 파일럿 순서 |
| 사내 브라우저 | 실제 UI smoke test | JS target·SSE fallback |
| 프로젝트 라이선스 | 소유자 결정 | 배포·외부 기여 조건 |

미확정 사항 때문에 문서 작성이나 Native 구조를 중단하지 않는다. 해당 항목은 capability와 명확한 실패 상태로 표현한다. 컨테이너 사용 가능 여부는 선택 실행기 검증 항목이지 출시 필수 전제가 아니다.

## 4. 변경 절차

영향 받는 요구 ID, 변경 이유, 대안, migration/호환 영향, 보안/품질/운영 비용, 수정할 schema/API/fixture/수용 테스트를 기록한다. 숫자 기본값을 바꿀 때 실제 측정과 단순 추정을 구분한다. 모든 질문을 사용자 폼 필수 입력으로 해결하려 하지 않는다.

## 5. 아직 구현하지 않은 것

W06에서 노드 등록·claim·lease와 워커 프로세스를 연결했다. 제품 빌드에 실제 분석기가 연결되는 단계는 W08이다. 워커 자동 증감(W07), 모델 검토, 보고서 발행과 품질 평가는 후속 작업이다. [노드 계약](28-w06-node-protocol.md)


## 6. W01 구현 결정 (2026-11-17)

**A11 — 첫 실행 기반의 경계.** REQ-004/026/030, NFR-001/006에 대해 버전·접근·인증 query의 실측과 미검증 능력을 분리한다. W01은 doctor와 로컬 진단 파일 뷰어이며 W02/W03 이전에 임시 in-memory run API를 도입하지 않는다. `start`를 정적 데모 서버 의미로 바꾸지 않는다. 진단 Schema/예시/validator와 TC-36~39를 함께 추가했다. DB/API migration은 없다. 상세 근거·대안·비용·테스트는 [W01 기록](21-w01-foundation.md)을 따른다.

**A12 — 모델 사용량과 선택.** 현재 개인 PC의 OpenCode 무료 모델 한도를 보존하도록 W01 모델 호출은 하지 않는다. 기본 OpenCode 방향(D10)을 유지한다. Codex/Claude Code는 향후 명시적 선택 어댑터 후보이며 workspace·권한·구조화 출력·취소 계약을 별도로 검증해야 한다. 동일 연결 방식이나 무료 한도를 가정하지 않고 자동 fallback을 추가하지 않는다.


**A13 — 개발 저장소의 CI 범위.** PR 검증 요청에 따라 CODEYE 자체의 단위·DB 통합·웹·계약 테스트를 GitHub Actions에서 실행한다. D02/REQ-032의 비목표는 분석 대상의 기존 CI 연결·수집·워크플로 생성 기능이며, 제품 자체를 개발하는 저장소의 자동 테스트를 금지한다는 뜻이 아니다. 제품 기능·분석 프로필·완료율 분모는 변경하지 않는다. CI는 모델 credential·모델 호출·배포 없이 read-only 저장소 권한으로 실행한다. Windows runner 단위 시험은 Windows 11의 NativeExecutor·DB·전체 플랫폼 수용 시험을 대체하지 않는다.


**A14 — W02 저장 불변조건.** pgx/v5 5.13.0과 내장 SQL migration을 사용한다. migration은 명시적 `codeye migrate`로만 실행하고 checksum·동시 적용·rollback을 검증한다. attempt는 leased/recovery_required/released, cleanup은 pending/uncertain/confirmed다. 만료/불확실 cleanup은 소유 슬롯을 유지하며 확인 후 failed로 정리한다. 명시적 failed/cancelled 재시도는 보관 snapshot과 확인된 cleanup만 허용한다. 동일 완료 receipt 재전송은 새 결과를 게시하지 않는다. event sequence와 entity version을 구분하고 task_executions로 재시도 전 이력을 보존한다. 영향 요구와 오류·호환·migration·테스트는 [W02 기록](23-w02-storage.md)을 따른다. 성공 보고서 게이트와 자원 admission은 각각 W09·W06/W07에서 추가한다.


**A15 — W03 개인 모드 인증.** loopback 전용 `codeye serve`를 제공하고 controller+agent를 뜻하는 `start`는 후속 supervisor로 유지한다. CLI에서 발급하는 일회용 launch link는 0600 파일에 저장하며 DB에는 token hash만 보관한다. 세션 cookie·same-origin Host/Origin·CSRF를 강제하고 비구성원은404로 존재를 숨긴다. 0002는 forward migration이며 배포/doctor에서 자동 적용하지 않는다. source 접근은 declared_unverified이고 worker 연결 전 queued를 성공으로 바꾸지 않는다. 브라우저 CSP 호환을 위해 JSON validator는 빌드 시 생성한다. [W03 근거와 시험](24-w03-api.md)을 따른다.


**A16 — W04 소스 보존 경계.** source root는 열린 디렉터리 핸들에서 하위 경로를 해석하고 플랫폼 data root는 전체 승인 source root 밖에 둔다. Git은 승인 HTTPS 또는 명시적 local root에서 plumbing 명령으로 객체를 읽는다. canonical manifest 전체 bytes의 hash로 limitations·시각·branch까지 묶으며 retained retry에서 원본을 재확보하지 않는다. fetch disk 감시는 soft limit이고 timeout은 recovery_required로 격리한다. private Git credential/CA profile과 snapshot의 노드 간 이동은 후속이다. [W04 계약·근거](25-w04-source.md)를 따른다.


**A17 — W05 종료 관측과 실행 경계.** NativeExecutor는 argv/cwd/env와 실행 outcome·cleanup을 분리한다. Linux는 leader PID를 reap 전까지 유지해 group signal의 PID재사용 위험을 줄이고, /proc 관측 실패에도 알려진 그룹을 종료하되 cleanup을 uncertain으로 남긴다. Windows는 suspended 생성 후 Job에 연결한 뒤 resume한다. OnStarted는 제한시간이 적용되는 사후 알림이며 callback 실패가 이미 실행된 코드의 부재를 의미하지 않는다. 경로 검증·env 제거는 OS sandbox가 아니고 CPU/메모리 한도는 advisory다. [W05 근거·시험](26-w05-native.md)을 따른다.


**A18 — W06 노드 프로토콜 계약과 0003.** REQ-018/019/022/035, NFR-002/003/006, TC-21에 대해 노드 HTTP를 다음으로 확정한다. (1) capacity는 bytes 단위(`memory_available_bytes`/`disk_available_bytes`), 명령 cursor는 노드별 단조 `sequence`(`last_command_sequence`)이며 command_id로 중복 제거한다. (2) claim 응답은 `{lease, job}`이고 lease에 claim 직후 `run_version`을 넣는다. complete·이벤트는 body의 `worker_id`와 인증 노드로 소유자를 대조한다. (3) 별도 snapshot 경로와 3단계 artifact upload 대신 `snapshot_fixed` 이벤트와 attempt 범위 단일 PUT을 쓴다. manifest bytes를 먼저 artifact로 올리고 그 hash가 snapshot digest와 같아야 한다. (4) 노드 경로는 Bearer만 받고 Origin이 있으면 거부한다. 다른 노드의 attempt는 404, 소유권·epoch·만료 불일치는 409 `STALE_ATTEMPT`다. (5) 자원 회계에서 CPU는 정책 예산(활성 예약 전체 차감)이다. 메모리·disk는 관측 여유에서 마지막 heartbeat 이후 예약만 뺀다. (6) 대기 사유와 프로젝트의 `execution_unavailable`은 저장 데이터에서 계산한다. 대안으로 OpenAPI 초안의 MB 단위·command ID cursor·lease 단독 응답을 검토했다. 단위 혼동, cursor 순서 보장 불가, job 재조회 왕복 때문에 채택하지 않았다. migration 0003은 forward 전용이다. 기존 W03 run 본문은 digest가 정확히 일치하는 scope=full 세 형태만 복원하고, 기존 Git snapshot(digest=commit)은 수정하지 않고 재시도 불가로 둔다. 비용은 claim 직렬화(전역 limits 행 잠금)이며 단일 controller 규모에서 허용한다. 상세 계약·시험은 [W06 기록](28-w06-node-protocol.md)을 따른다.

**A19 — 실제 분석기 판정 시점.** 워커가 task를 시작한 뒤 모듈·도구 probe에서 미지원 입력이나 정책 제외를 확인할 수 있다. 이때 `running → unsupported/skipped_policy`를 기록한다. 두 상태는 종료 상태이며 성공 수에 포함되지 않는다. 상태 어휘와 기존 DB 행은 유지된다. [Go vet 시험](29-w08-go-vet.md)
