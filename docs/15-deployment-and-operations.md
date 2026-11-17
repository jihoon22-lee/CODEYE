# 15. 배포와 운영

## 1. 지원 목표와 선행조건

개인 PC는 Native 서버·에이전트·워커·PostgreSQL·승인된 모델 연결·필요 분석 도구로 시작한다. 서버는 RHEL 8.10과 내부 미러·CA·NFS 조건을 우선 검증한다. Docker/Podman은 필요 조건이 아니다. 배포 manifest에 실제 OS/arch·빌드 ABI·toolchain·DB·모델 adapter 버전을 고정한다.

Go 서비스 바이너리와 정적 웹 파일을 배포하되 분석 도구 런타임은 별도다. cgo/동적 라이브러리를 사용하면 대상 glibc·시스템 라이브러리 호환성을 실제 확인한다. 최신 빌드 머신에서 컴파일됐다는 사실만으로 RHEL 8 호환을 가정하지 않는다.

## 2. 개인 모드 UX

`codeye doctor`는 [W01 범위](21-w01-foundation.md)에서 구현했다. `codeye migrate`와 loopback controller 전용 `codeye serve`는 구현했다([W03 실행 방법](24-w03-api.md)). `codeye start`, `codeye stop`, `codeye agent enroll`은 아직 설계 계약이다. doctor는 DB·작업 디스크·toolchain을 probe하고 모델·프로세스 제어의 미검증 상태를 명시한다. W05의 OS별 프로세스 종료 시험은 [실행기 검증 기록](26-w05-native.md)에 있다. 실모델 연결 시험은 W10에서 수행한다. start는 local controller와 agent를 기동하며 DB가 준비되지 않았으면 구체적인 설치/연결 안내를 한다.

PostgreSQL을 몰래 시스템 서비스로 설치하지 않는다. 명시적 사용자 승인 아래 Native 설치 가이드를 제공하고 이후 서비스 시작을 단순화한다. 기본 listen은 loopback이다. 브라우저를 닫아도 서비스를 유지하며 stop은 실행 중 작업 drain 또는 명시적 cancel 선택을 지원한다.

## 3. 서버/노드 배포

controller는 단일 활성 인스턴스로 시작한다. DB와 artifact store를 지정하고 노드 에이전트를 운영자가 직접 설치한다. Windows Service 또는 승인된 작업 실행 방식, Linux systemd/사용자 service 등 로그아웃 후 지속 실행 가능한 방법을 환경에 맞게 선택한다. 서버가 노드에 SSH하지 않는다.

enrollment token은 짧은 수명·1회용이며 node identity와 pool·신뢰 범위를 부여한다. 에이전트 업데이트는 drain→binary 교체→protocol 확인→재등록 없이 복귀 순서를 따른다. 최소/최대 정책은 controller가 유지한다.

## 4. 저장 위치

로컬 디스크에는 DB data, workspace, tool/runtime cache, 임시 결과를 우선 둔다. NFS는 원본 source와 선택적 보관 산출물에 사용한다. DB data를 NFS로 옮기는 것은 별도 DB 운영 검증 없이 기본 지원하지 않는다. node cache를 공용 NFS에 두어 잠금·성능 문제가 발생하는 것을 기본 설계로 삼지 않는다.

디스크 quota는 snapshot·workspace·cache·logs별로 둔다. 용량 부족 시 새 작업을 막고 기존 작업의 산출물 보호를 우선한다. 무조건 가장 오래된 파일 삭제 대신 retention·참조·pin 여부를 확인한다.

## 5. 폐쇄망 패키징

버전·hash가 고정된 서비스 바이너리/웹 자산/DB migration/규칙팩/도구 manifest를 제공한다. 폰트·아이콘·JS·CSS·차트에 외부 CDN 의존성을 두지 않는다. 도구 바이너리 재배포가 불가하면 사내 미러에서 설치할 경로와 검증 정보를 제공한다.

분석기 catalog, 언어 toolchain, 프로젝트 의존성 mirror는 구분한다. 미러에 없는 버전은 unavailable로 표시하고 외부 다운로드로 해결하지 않는다. CA 검증을 끄는 방식으로 연결 문제를 감추지 않는다. 설정의 `.invalid` 주소와 예시 경로는 실제 운영 정보로 교체하되 Git에 secret을 올리지 않는다.

## 6. 관측성

웹/API 지연, queue wait, active/idle/draining workers, capacity 부족 이유, attempt retry, stale write 거부, tool failure/parser failure, 모델 latency/429/timeouts, artifact queue/용량, 보고서 생성 실패를 측정한다. endpoint별 모델 사용량은 공급자가 제공한 값과 추정치를 구분한다. 코드·프롬프트 전문을 일반 운영 로그에 넣지 않는다.

## 7. 백업과 복구

DB backup과 finalized artifact manifest를 같은 논리 시점으로 맞춘다. DB만 복원하면 evidence가 누락될 수 있다. 최소 복구 시험은 프로젝트·run·report·evidence 링크·ACL·예약 중복 방지를 확인한다. 복원 직후 과거 lease를 살리지 않고 노드와 reconciliation한다. 백업·복구 목표 시간은 실제 데이터 규모 측정 후 결정한다.

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
