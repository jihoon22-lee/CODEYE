# 18. 수용 테스트와 요구사항 추적

여기에 정의한 TC는 구현 후 실행할 시험이다. 현재 보조 validator/fixture 결과를 이 전체 시험의 통과로 해석하지 않는다.

## 1. 입력·환경

| TC | Given / When | Then |
|---|---|---|
| TC-01 | 설명 없는 Git/local 프로젝트 등록 | 필수 추가 문서 없이 요청 가능, 자동 탐색 및 추정 표시 |
| TC-02 | OS 계정은 읽지만 웹 사용자는 권한 없는 경로 | 등록·목록·보고서·SSE 모두 접근 거부 |
| TC-03 | 큐 대기 중 브랜치 변경, 확보 후 다시 변경 | 확보 시 SHA를 사용하고 후속 변경 미반영; 재시도도 동일 SHA |
| TC-04 | local 복사 중 파일 변경·symlink root 탈출 | 변경 재시도/불안정 실패, 외부 링크 미추적, 원본 미수정 |
| TC-05 | monorepo·문서 없음·문서 충돌·버전 불일치 | 모듈별 사실/추론·제약 기록, 가능한 분석 계속 |
| TC-06 | C++ compile DB가 원본 절대 경로를 포함 | workspace 매핑·include 권한 확인, 분석 TU 범위 표시 |

## 2. 도구·LLM·보고서

| TC | Given / When | Then |
|---|---|---|
| TC-07 | 도구가 findings용 nonzero exit를 반환 | tool crash와 구분하고 정상 observations 생성 |
| TC-08 | 출력 손상·일부 파싱·timeout | raw 보존, 실패 범위 표시, 0건 통과 금지 |
| TC-09 | 비슷한 경고 두 개와 다른 원인 같은 줄 | 동일 원인만 묶고 서로 다른 조건은 유지 |
| TC-10 | 후보를 반박하는 상위 finally/validation 존재 | 검토 후 기각/범위 축소, 근거 이력 유지 |
| TC-11 | 모델이 없는 파일·행·실행 결과를 주장 | 증거 검증 거부, 재현 상태 승격 차단 |
| TC-12 | 모델 timeout·JSON 오류·permission ask | 제한 재시도 후 partial, 도구 기반 보고서 열람 가능 |
| TC-13 | 원시 도구 수와 finding 수가 다름 | 요약/상세/export 통계 일치, 두 숫자 구분 |
| TC-14 | 이전 finding이 이번 도구 실패로 안 보임 | resolved가 아니라 absent_unverified |
| TC-15 | 근거 코드에 HTML·secret·외부 이미지 | escape/redact, 외부 fetch 없음, export 정책 적용 |
| TC-16 | 문서 prompt injection이 토큰/권한 상승 요구 | 실행 정책 유지, secret 미노출, 공격 자료로만 취급 |

## 3. 실행·워커·예약

| TC | Given / When | Then |
|---|---|---|
| TC-17 | Native에서 도구/build/test 실행 | 원본 hash 불변, 작업별 환경·자원·로그 독립 |
| TC-18 | 손자 프로세스를 만든 작업 취소/timeout | 그룹 종료 확인, 불확실하면 recovery_required |
| TC-19 | min0/max2, 작업 유입 후 유휴 | 0→최대2→0, agent/controller는 살아 있음 |
| TC-20 | busy3 상태에서 max1로 축소 | 작업 강제 종료 없이 drain, 새 작업 제한 |
| TC-21 | 두 agent가 같은 run을 동시에 claim | 한 활성 epoch만 소유, 노드/전역 예산 중복 차감 없음 |
| TC-22 | node partition 후 오래된 complete 도착 | lease fence 409, 최신 보고서 미덮어쓰기 |
| TC-23 | controller/agent 재시작·PID 재사용 | journal/DB reconciliation, 중복 워커 방지, 다른 PID 오살 없음 |
| TC-24 | 모델 endpoint 포화·CPU 여유 | 모델 동시 제한 유지, 가능한 CPU task는 진행, 과도한 worker 증가 억제 |
| TC-25 | 같은 예약 중복, 실행 중 여러 occurrence, 서버 정지 | 유일성·coalesce_latest·latest_once 적용 |
| TC-26 | browser 종료/reconnect와 plan 추가 task | 실행 유지, cursor 복원, revision과 새 진행 분모 표시 |

## 4. 연결·운영

| TC | Given / When | Then |
|---|---|---|
| TC-27 | webhook이 없지만 repository readable | 수동/예약 사용 가능, PR 기능만 연결 미완료 |
| TC-28 | 잘못된 HMAC·중복 delivery·무권한 mention | 요청 차단/멱등 처리, 승인된 사용자만 실행 |
| TC-29 | PR head 변경 후 이전 결과 완료·댓글 API 실패 | stale 표기, 최신 댓글 미덮어쓰기, outbox만 재시도 |
| TC-30 | 폐쇄망에서 없는 패키지·모델·외부 CDN 요청 | 외부 fallback 없음, 명확한 부분 실패, UI는 로컬 자산 사용 |
| TC-31 | 권한 철회/삭제·code-free export | source/report/SSE 차단, export 내용과 metadata 민감값 제거 |
| TC-32 | DB+artifact backup 복원 | 근거·ACL·예약 중복·lease 재조정 정상 |
| TC-33 | schema/지침/모델/규칙 변경 | revision/digest·holdout 회귀 결과 기록 |
| TC-34 | 원격 OpenCode가 다른 workspace를 봄 | marker/hash 불일치 거부, 다른 개인 경로 fallback 없음 |
| TC-35 | 문서·코드·서비스 설정의 기능 검토 | 기존 CI 연결 및 초기 CVE 기능이 없음 |

## 5. 요구사항 추적표

| 요구 | 주요 문서 | 수용 테스트 |
|---|---|---|
| REQ-001,004,005 | 05,07,12 | TC-01,05 |
| REQ-002,015 | 05,08,14 | TC-02,04,17 |
| REQ-003 | 04,05 | TC-03,04 |
| REQ-006,007,008 | 04,06 | TC-06,07,08 |
| REQ-009,010,011 | 02,07,10 | TC-09,10,11,16 |
| REQ-012,013 | 10,12 | TC-12,13,15 |
| REQ-014 | 05,08 | TC-17,18 |
| REQ-016,017,034 | 11,12 | TC-26 |
| REQ-018,019 | 03,09 | TC-21,24 |
| REQ-020,021 | 09 | TC-19,20 |
| REQ-022,035 | 04,09,11 | TC-18,21,22,23 |
| REQ-023 | 09 | TC-25 |
| REQ-024,025 | 13 | TC-27,28,29 |
| REQ-026 | 07 | TC-12,34 |
| REQ-027 | 07,16 | TC-33 |
| REQ-028,029 | 10,12 | TC-14,31 |
| REQ-030 | 14,15 | TC-30 |
| REQ-031 | 12,14 | TC-15,31 |
| REQ-032,033 | 00,01 | TC-35 |
| NFR-001,009 | 08,15 | TC-17,18,23 |
| NFR-002,003 | 09,11,15 | TC-21,23,25,32 |
| NFR-004,005 | 12 | TC-26, 아래 UI 성능 시험 |
| NFR-006,007 | 10,14 | TC-02,15,22,31,32 |
| NFR-008,010 | 07,16 | TC-11,12,24,33 |

## 6. 고위험 시나리오 세부

**Lease partition:** A가 실행 중인 상태에서 controller 통신을 끊는다. agent가 마지막 갱신 뒤 안전 여유 안에 새 task를 멈추는지 확인한다. 종료 불확실한 test를 자동 중복 실행하지 않는지 확인한다. A의 이전 epoch 완료 요청은 거부되고 새 attempt 결과는 유지돼야 한다.

**취소:** worker→shell→test→자식 프로세스 구조를 만든다. 정상 종료, 응답 없는 자식, agent crash, PID 재사용을 각각 시험한다. 종료된 process group/Job의 결과와 cleanup 상태를 검증하며 로그만 보고 성공 처리하지 않는다.

**소스 일관성:** 파일이 추가/삭제/수정되는 중 복사한다. 정상 안정 사본, 재시도 후 성공, source_unstable, 외부 링크 차단을 각각 확인한다. NFS 단순 복사가 원자적이라는 문구가 UI에 없어야 한다.

**UI 성능:** 큰 로그/다수 findings fixture에서 목록 페이지·검색·필터·키보드 이동을 시험한다. 정상 연결의 이벤트 반영 지연 목표를 측정하고 백엔드 처리 지연과 UI 지연을 구분한다. 결과는 OS·브라우저·데이터 크기와 함께 기록한다.

## 7. 문서 보조 검증과 제품 시험의 차이

현재 `scripts/validate_bundle.py`는 문서 링크·Schema·예시·계획 DAG·보고서 수치·범위 참조를 검사한다. `fixtures/cleanup/test_cleanup.py`는 합성 결함 전후를 검사한다. 이 둘의 성공을 TC-01~35 전체 통과나 실제 분석 성능 확보로 보고하지 않는다.


## 8. W01 기반 수용 테스트

기존 TC-01~35의 전체 제품 경로와 구분한다. W01이 구현한 범위는 아래 시험과 TC-35 범위 점검이며, 상위 요구사항 전체 완료를 뜻하지 않는다.

| TC | Given / When | Then | 연결 |
|---|---|---|---|
| TC-36 | Native doctor 실행, 도구 없음·실패·출력 초과·timeout | OS/arch와 실제 버전만 기록, 실패/미검증 구분, bounded probe, secret 상속·원시 출력 노출 없음 | REQ-004,030; NFR-001,006 |
| TC-37 | DB 미설정·정상·잘못된 인증·없는 DB·허용되지 않은 TLS 설정 | 인증 query 성공만 available, 고정 실패 reason, credential argv/결과 비노출, 설정 없는 네트워크 호출 없음 | REQ-030; NFR-006 |
| TC-38 | OpenCode CLI만 설치된 W01 | version_only, 모델·workspace·취소는 unverified, 모델 호출/자동 fallback 없음 | REQ-026,030 |
| TC-39 | 웹에서 doctor JSON 읽기, 손상·미지원 schema·중복 ID·파일 교체 | 실제 결과만 표시, 오류와 미검증을 성공으로 바꾸지 않음, 파일 서버 전송 없음, 최신 선택 유지 | REQ-013,030; NFR-006 |

실행 결과와 Windows/RHEL·실모델·브라우저 미실행 범위는 [W01 검증 기록](21-w01-foundation.md)에 기록한다.


## 9. W02 저장 계층 수용 시험

| TC | 검증 | 요구 연결 |
|---|---|---|
| TC-40 | 빈 DB 적용·재적용·동시 migration, checksum/future/gap 거부, 실패 rollback | NFR-003 |
| TC-41 | 프로젝트 ACL·권한 철회·immutable revision·원자적 생성 | REQ-002,003 |
| TC-42 | 동시 멱등 enqueue·body 충돌·상태/event rollback·cursor 순서 | REQ-022; NFR-002,003 |
| TC-43 | 동시 소유권 경쟁·node/epoch 불일치·만료 fencing·불확실 cleanup·명시적 재시도 | REQ-022,035 |
| TC-44 | snapshot 한 번 고정·동일 입력 재시도·task 실행 이력·다른 run/project 근거 차단 | REQ-003,013; NFR-007 |
| TC-45 | 성공을 finalizing에 유지·미실행/실패 구분·오류 credential 비노출 | REQ-013; NFR-006 |

[W02 구현 기록](23-w02-storage.md)의 실제 결과와 제한을 따른다. 이 시험은 저장 계층이며 agent·NativeExecutor·artifact bytes·보고서 E2E 통과를 뜻하지 않는다.


## 10. W03 API·인증 수용 시험

| TC | 검증 | 요구 연결 |
|---|---|---|
| TC-46 | loopback 강제, Host/Origin/CSRF 거부 | NFR-006 |
| TC-47 | 일회용 launch token, 동시 교환·비활성화, 세션 만료/폐기 | REQ-002; NFR-006 |
| TC-48 | 설명 없는 local/Git 선언 등록, root grant, 소스 미검증 표시 | REQ-001,002,005 |
| TC-49 | 비구성원404·viewer403·철회 후 접근 거부 | REQ-002 |
| TC-50 | 멱등202·충돌409·DB실패503·서버 재접속 후 상태 보존 | REQ-022; NFR-002,003 |
| TC-51 | readiness 읽기 전용, serve의 구/신/변조 schema 거부 | NFR-003 |
| TC-52 | 정적 파일 경계·CSP·비밀값 비노출·실브라우저 요청/취소 | NFR-005,006 |

[W03 기록](24-w03-api.md)의 실행 환경·검증 범위를 따른다. W04 이후의 실제 분석 실행은 별도 수용 증거다.


## 11. W04 소스 확보 수용 시험

| TC | 검증 | 요구 연결 |
|---|---|---|
| TC-53 | 승인 root 원자적 경계·symlink/FIFO 교체·credential 제외·원본 아래 data root 거부 | REQ-002,015; TC-04 |
| TC-54 | 변경 감지 재시도·불안정 실패·manifest 무결성·독립 workspace·retained retry | REQ-003,015; TC-03,04 |
| TC-55 | 실제 Git 확보 SHA 고정·filter 미실행·LFS/submodule 공백·host/전송 제한·soft fetch quota | REQ-003,024,030 |

[W04 기록](25-w04-source.md)의 실행 범위와 NFS/Windows 실기·private Git 미검증 제한을 따른다.


## 12. W05 NativeExecutor 수용 시험

| TC | 검증 | 요구 연결 |
|---|---|---|
| TC-56 | literal argv·최소 env·원본/snapshot/cwd 경계·비밀값과 잘린 출력 마스킹 | REQ-014,015; NFR-006 |
| TC-57 | timeout/cancel 후 자식·손자 OS 종료, 잔류/탈출·관측 실패는 uncertain | REQ-022,035; TC-18 |
| TC-58 | Windows suspended 생성→Job 연결→resume·breakaway 거부, 미지원과 enforced limit 구분 | NFR-001,009 |

[W05 실행 기록](26-w05-native.md)을 따른다. 프로세스 그룹/Job은 OS 권한 샌드박스가 아니며 RHEL/Windows11 실기와 agent crash reconciliation은 별도다.


## 13. W06 노드와 워커 수용 시험

| TC | 검증 | 요구 연결 |
|---|---|---|
| TC-59 | 일회용 등록, 철회·만료, 사용자와 노드 credential 분리 | REQ-019, NFR-006 |
| TC-60 | 두 노드의 동시 claim, 한 epoch·예약·이벤트, 재전송과 실패 rollback | REQ-018/019/022, TC-21 |
| TC-61 | 호환성·root 권한·자원과 실제 대기 사유, 비호환 요청 뒤의 호환 작업 선택 | REQ-002/018/019 |
| TC-62 | 갱신 중단, stale 쓰기 409, cleanup 불확실 시 예약 유지 | REQ-022/035, TC-22 |
| TC-63 | 워커 환경·IPC에서 노드 비밀값 제외, 취소 후 자손 종료, journal·PID 재사용 복구 | REQ-015/022, TC-18/23 |
| TC-64 | 고정 snapshot 재시도와 hash·크기·attempt 소유권에 묶인 artifact 접수 | REQ-003/035, NFR-007 |
| TC-65 | 기존 0002 DB 이행, Linux 실제 두 에이전트에서 취소 후 다음 run 접수, Windows Job 소유자 종료 | NFR-001/003/009 |

실행 명령은 `python3 scripts/test_postgres.py --web --nodes`다. 노드 수용 시험은 전용 빌드의 시험용 handler로 프로토콜과 OS 동작을 검사한다. [저장·HTTP 시험](28-w06-node-protocol.md) · [워커 프로세스 시험](28-w06-agent-runtime.md)
