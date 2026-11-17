# 17. 구현 작업 계획

현재 W01 환경 진단·프로젝트 기반을 구현했다([실행 증거와 제한](21-w01-foundation.md)). W02~W31은 미착수이며 다음 작업은 W02다. W01의 `unverified` 결과는 W05/W10 실행 능력 완료가 아니다. 문서·schema·fixture의 존재를 구현 완료로 체크하지 않는다. 일정 추정 대신 선행 관계와 완료 조건으로 관리한다. 목표는 인프라를 오래 쌓은 뒤 품질을 보는 것이 아니라 작은 실제 분석 경로를 먼저 검증하는 것이다.

## 1. R0: 개인 PC의 실동작 수직 경로

| ID | 작업 | 선행 | 산출물·완료 조건 |
|---|---|---|---|
| W01 | 환경 doctor·프로젝트 skeleton — 기반 구현 완료 | 없음 | 실제 OS/도구/DB/모델 능력 결과. 실패 원인을 구분하고 Docker 요구 없음 |
| W02 | 도메인 계약·DB migration | W01 | run/attempt/task/evidence/project ACL·event·lease의 트랜잭션 테스트 |
| W03 | 최소 API·인증·웹 뼈대 | W02 | 프로젝트 등록→run 202→상태 조회. 무인증 LAN 공개 금지 |
| W04 | SourceAdapter Git/local | W02 | acquire 시 입력 고정, 원본 미변경, manifest·불안정 복사 검사 |
| W05 | NativeExecutor | W01 | argv/env/cwd, timeout, stdout cap, Windows/Linux 프로세스 그룹 종료 |
| W06 | Agent·worker protocol | W02,W05 | enroll/heartbeat/claim/epoch, 두 에이전트로 중복 claim 방지 |
| W07 | Scheduler·autoscaler | W06 | min/max, 0→N→0, drain, resource admission, lease expiry·late result 시험 |
| W08 | 첫 언어 분석 어댑터 | W04,W05 | 실제 개인 프로젝트에 맞는 한 언어 선정; raw→normalize·실패 parser fixture |
| W09 | 구조화 보고서 renderer | W03,W08 | LLM 없이도 task 상태·원시 근거·공백이 보이는 보고서 |
| W10 | OpenCode adapter 실연동 | W04,W06 | 실제 버전 계약, workspace digest, model request/cancel/permission block 확인 |
| W11 | Context·Investigator 지침 | W08,W10 | 문서 없는 코드와 문서 있는 코드 모두 근거 있는 후보 생성 |
| W12 | Verifier·증거 검증 | W11 | 반례로 후보 철회, 허위 path·재현 주장 차단, 모델 실패 partial |
| W13 | 보고서 종합·상세 UI | W09,W12 | 요약→영역→근거, 숫자 일치, 미확정 표시, export |
| W14 | R0 회귀·개인 파일럿 | W07,W13 | 최소 합성 집합+승인된 개인 프로젝트에서 실제 end-to-end 실행 기록 |

R0의 두 에이전트 시험은 한 머신에서 별도 node identity로 먼저 수행할 수 있다. 실제 다중 머신·NFS·네트워크 partition 보증을 대체하지 않는다. Windows와 RHEL 중 개발에 사용하지 않은 환경의 시험은 미실행으로 명시한다.

## 2. R1: 개인 실용판과 분석 깊이

| ID | 작업 | 선행 | 완료 조건 |
|---|---|---|---|
| W15 | 모듈·문서·환경 자동 탐색 | W14 | monorepo, 하위 AGENTS, 상충 문서, runtime mismatch, optional attachment |
| W16 | C/C++ 분석 팩 | W15 | compile DB path mapping, 생성 헤더·flags, 부분 TU 범위, clang/cppcheck 정규화 |
| W17 | Python 분석 팩 | W15 | Ruff+타입 검사+보안/오류 흐름, interpreter/stub/import 공백 표시 |
| W18 | JS/TS 분석 팩 | W15 | tsconfig/workspace/Node, type-aware lint, async·경계 검토 |
| W19 | Go 분석 팩 | W15 | vet/Staticcheck, tags/CGO, context·goroutine·오류 경로 |
| W20 | Rust 분석 팩 | W15 | check/clippy, features/target, build script 신뢰, unsafe·panic 검토 |
| W21 | 구조/복잡도·비밀정보 팩 | W15 | 지표를 결함으로 오인하지 않음, secret raw redaction, 중복 엔진 제거 |
| W22 | 예약·진행·재접속 | W07,W13 | timezone/overlap/misfire, 계획 revision 진행률, SSE gap 복원 |
| W23 | 이력·비교·사람 검토 | W13 | fingerprint, absent≠fixed, disposition 이유·기간·권한 |
| W24 | 품질 평가 runner | W12,W16-W21 | 정답 차단, holdout, 지표/표본·모델 변동·보고서 읽기 평가 |
| W25 | 개인 패키징·운영 검증 | W22-W24 | Native start/stop/doctor, disk quota, DB·artifact backup/복원, 사용 가이드 |

다섯 언어의 첫 공통 탐색 지원은 빠르게 넣을 수 있지만 정밀 어댑터 완료는 위 시험이 있어야 한다. 실제 첫 프로젝트의 언어에 맞춰 W16~W20 순서를 바꿀 수 있다. '다섯 언어 지원' 표시는 탐색/기본/정밀/실행 검증 수준을 분리한다.

## 3. R2: 부서 공개

| ID | 작업 | 선행 | 완료 조건 |
|---|---|---|---|
| W26 | 다중 사용자·권한 강화 | W25 | project/source-root/artifact/export/SSE 격리와 감사 테스트 |
| W27 | GHES/GitHub App | W26 | 연결 가이드, 최소 권한, webhook HMAC·dedupe·비동기 응답 |
| W28 | PR·댓글 분석 | W27 | SHA 고정, 호출자 권한, fork 정책, stale 댓글 방지, outbox |
| W29 | 다중 물리 노드 검증 | W25,W26 | 실제 node capability 배정, NFS 접근 차이, partition·복구·drain |
| W30 | 폐쇄망 배포팩 | W29 | 내부 mirror/CA, 외부 fallback 없음, offline assets, 툴 버전 계약 |
| W31 | 운영 수용·공개 | W28,W30 | 장애 훈련, backup 복원, 권한 철회, 품질 baseline·제한 문서 |

## 4. 후속 선택 작업

ContainerExecutor, 고비용 interfile 분석, 추가 OS, 인프라 CapacityProvider, PDF export는 필요에 따라 별도 작업으로 만든다. 기존 CI 통합과 의존성 CVE 검사를 자동으로 이 단계에 끼워 넣지 않는다. 제품 범위 변경은 소유자 결정이 필요하다.

## 5. 작업 티켓 형식

각 작업에는 목적, 요구 ID, 입력/출력, 변경 모듈, 선행 작업, 실패 시나리오, API/schema 영향, 수용 테스트, 관측 지표, rollback·마이그레이션, 완료 증거를 적는다. 구현 설명만 있고 실제 시험 결과가 없으면 done으로 처리하지 않는다.

## 6. 권장 개발 순서

첫 보고서 데이터와 화면은 W09에서 일찍 만든다. 지침 W11/W12와 품질 평가를 인프라 완료 뒤로 무한 연기하지 않는다. 분산 계약은 처음부터 구현하되 한 run을 여러 머신에 쪼개는 복잡성은 넣지 않는다. UI 데모 데이터와 실제 결과의 모드를 명확히 구분한다.

개인 프로젝트 이름은 파일럿 후보일 뿐 현재 소스·언어·규모를 확인한 결과가 아니다. 실제 저장소와 권한을 확인하고 immutable snapshot으로 기준을 만든다. 사용자 프로젝트에 결함 fixture를 몰래 삽입하지 않는다.

## 7. 단계 종료 증거

R0는 실제 입력·명령·모델 연결·보고서·취소·autoscale 로그, R1은 언어별 계약 시험과 품질 baseline, R2는 권한·분산·폐쇄망 복구 시험을 남긴다. 코드 테스트와 제품 분석 정확도 평가를 별도 결과로 보고한다.
