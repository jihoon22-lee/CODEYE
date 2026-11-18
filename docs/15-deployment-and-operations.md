# 15. 배포와 운영

## 1. 지원 목표와 선행조건

개인 PC는 Native 서버·에이전트·워커·PostgreSQL·승인된 모델 연결·필요 분석 도구로 시작한다. 서버는 RHEL 8.10과 내부 미러·CA·NFS 조건을 우선 검증한다. Docker/Podman은 필요 조건이 아니다. 배포 manifest에 실제 OS/arch·빌드 ABI·toolchain·DB·모델 adapter 버전을 고정한다.

Go 서비스 바이너리와 정적 웹 파일을 배포하되 분석 도구 런타임은 별도다. cgo/동적 라이브러리를 사용하면 대상 glibc·시스템 라이브러리 호환성을 실제 확인한다. 최신 빌드 머신에서 컴파일됐다는 사실만으로 RHEL 8 호환을 가정하지 않는다.

## 2. 개인 모드 UX

R0에서는 운영자가 PostgreSQL 연결을 준비해 `codeye migrate`로 schema를 적용하고 `codeye serve`로 loopback 서버와 정적 웹을 띄운다. 일회용 토큰을 발급한 뒤 별도 `codeye agent` 프로세스를 시작하면 등록과 heartbeat가 진행된다. `--autoscale`은 풀 정책에 맞게 워커를 시작·정리한다. [로그인과 서버](24-w03-api.md) · [노드 토큰](28-w06-node-protocol.md) · [에이전트 옵션](28-w06-agent-runtime.md) · [풀 정책](30-w07-scheduler.md)

호스트 진단은 `codeye doctor`, 승인 모델의 실요청 확인은 `codeye model probe`로 수행한다. 모델 구성을 선택하지 않은 환경에서 다른 서비스로 우회하지 않는다. [진단 결과 읽기](21-w01-foundation.md) · [모델 검증](32-w10-opencode.md)

W25부터 개인 모드는 설정 파일 하나로 `codeye start`·`stop`·`status`를 쓴다(Linux). 저장 영역 상한, 같은 시점 백업·복원, `codeye doctor --config`의 운영 점검이 함께 있다. 관리형 PostgreSQL과 Windows의 start/stop은 없다. [개인 사용 안내서](48-user-guide.md) · [W25 기록](47-w25-packaging-and-operations.md). 아래의 서버 배포·폐쇄망 운영 절은 후속 지원을 설계할 때의 조건이다.

## 3. 서버/노드 배포

controller는 단일 활성 인스턴스로 시작한다. DB와 artifact store를 지정하고 노드 에이전트를 운영자가 직접 설치한다. Windows Service 또는 승인된 작업 실행 방식, Linux systemd/사용자 service 등 로그아웃 후 지속 실행 가능한 방법을 환경에 맞게 선택한다. 서버가 노드에 SSH하지 않는다.

enrollment token은 짧은 수명·1회용이며 node identity와 pool·신뢰 범위를 부여한다. 에이전트 업데이트는 drain→binary 교체→protocol 확인→재등록 없이 복귀 순서를 따른다. 최소/최대 정책은 controller가 유지한다.

## 4. 저장 위치

로컬 디스크에는 DB data, workspace, tool/runtime cache, 임시 결과를 우선 둔다. NFS는 원본 source와 선택적 보관 산출물에 사용한다. DB data를 NFS로 옮기는 것은 별도 DB 운영 검증 없이 기본 지원하지 않는다. node cache를 공용 NFS에 두어 잠금·성능 문제가 발생하는 것을 기본 설계로 삼지 않는다.

디스크 quota는 snapshot·workspace·cache·logs별로 둔다. 용량 부족 시 새 작업을 막고 기존 작업의 산출물 보호를 우선한다. 무조건 가장 오래된 파일 삭제 대신 retention·참조·pin 여부를 확인한다. W25는 영역별 상한과 새 작업 거절까지 구현했고 삭제·retention은 없다([W25 §4](47-w25-packaging-and-operations.md#4-저장-영역-상한)).

## 5. 폐쇄망 패키징

버전·hash가 고정된 서비스 바이너리/웹 자산/DB migration/규칙팩/도구 manifest를 제공한다. 폰트·아이콘·JS·CSS·차트에 외부 CDN 의존성을 두지 않는다. 도구 바이너리 재배포가 불가하면 사내 미러에서 설치할 경로와 검증 정보를 제공한다.

분석기 catalog, 언어 toolchain, 프로젝트 의존성 mirror는 구분한다. 미러에 없는 버전은 unavailable로 표시하고 외부 다운로드로 해결하지 않는다. CA 검증을 끄는 방식으로 연결 문제를 감추지 않는다. 설정의 `.invalid` 주소와 예시 경로는 실제 운영 정보로 교체하되 Git에 secret을 올리지 않는다.

## 6. 관측성

웹/API 지연, queue wait, active/idle/draining workers, capacity 부족 이유, attempt retry, stale write 거부, tool failure/parser failure, 모델 latency/429/timeouts, artifact queue/용량, 보고서 생성 실패를 측정한다. endpoint별 모델 사용량은 공급자가 제공한 값과 추정치를 구분한다. 코드·프롬프트 전문을 일반 운영 로그에 넣지 않는다.

## 7. 백업과 복구

DB backup과 finalized artifact manifest를 같은 논리 시점으로 맞춘다. DB만 복원하면 evidence가 누락될 수 있다. 최소 복구 시험은 프로젝트·run·report·evidence 링크·ACL·예약 중복 방지를 확인한다. 복원 직후 과거 lease를 살리지 않고 노드와 reconciliation한다. 백업·복구 목표 시간은 실제 데이터 규모 측정 후 결정한다. 개인 모드의 구현과 복구 시험은 [W25 §5](47-w25-packaging-and-operations.md#5-백업과-복원)에 있다.

## 8. 운영 상황별 대응

| 상황 | 기본 조치 |
|---|---|
| 모델 응답 실패 | 도구 결과 보존, 모델 재시도 한도, partial 보고서 |
| node offline | 새 배정 중지, lease fence, recovery_required 확인 |
| DB unavailable | 신규 요청 503, agent는 lease 안전 정책으로 중단 |
| NFS 지연 | acquire timeout, 원본 불안정/권한 구분, 서버 전체 block 금지 |
| tool version 변경 | capability 갱신, 계약 시험 후 catalog revision |
| max worker 축소 | drain, 진행 작업 강제 종료하지 않음 |
| artifact quota 초과 | 신규 생성 제한, 참조 없는 임시 업로드 정리 |

## 9. 배포 전 체크

인증/TLS, source root 권한, 도구 사용 조건, Native trust, timeout/취소, 자원 reserve, LLM 한도, 외부 통신 금지, backup 복원, export redaction, 사용자 안내가 준비됐는지 확인한다. [개인용 설정](../examples/platform.yaml)은 출발점일 뿐 실제 머신 용량을 측정해 조정한다.
