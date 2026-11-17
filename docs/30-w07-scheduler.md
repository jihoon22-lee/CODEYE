# W07 워커 풀과 자동 증감

## 실행 방법

controller를 실행한 DB에 운영자 CLI로 정책을 적용한다. 등록된 노드의 pool 이름이 정책의 이름과 일치해야 한다. 다음 명령은 한 pool에서 작업이 생기면 최대 두 워커를 시작하고 유휴 상태가 지속되면 모두 종료한다.

```sh
codeye scheduler set --global-max-running 2 --project-max-running 1
codeye pool set --name default --min-workers 0 --max-workers 2 \
  --idle-timeout-seconds 180 --scale-up-cooldown-seconds 10 --scale-down-cooldown-seconds 60
codeye pool list
codeye serve --reconcile-interval 5s
```

노드의 기존 등록·소스·도구 인수에 `--autoscale --max-workers 2`를 추가한다. 기존 `--workers N` 모드는 고정 수를 유지한다. 자동 모드는 최초 한 번 워커 handshake로 실제 handler를 확인한 뒤 그 프로세스를 종료한다. 이후 시작 명령을 받으면 새 워커를 만들고 검증된 능력을 heartbeat에 보고한다. 자동 모드의 로컬 상한은 1~16이다.

## 배정과 정책 변경

- 배정은 PostgreSQL 트랜잭션에서 전역·프로젝트·pool·노드 상한, heartbeat, CPU·RAM·disk, snapshot 위치, 요청자와 노드의 소스 접근을 확인한다. 정리 확인 전의 예약도 활성 수에 포함한다.
- 프로젝트별 기본 상한은 1이다. 마지막 배정 시각이 오래된 프로젝트가 우선이며, 그 안에서는 오래 기다린 요청부터 찾는다.
- 각 pool은 하나의 조정 트랜잭션에서 현재 정책과 워커 상태를 읽고 시작·종료 명령, 조정 epoch, 쿨다운 시각을 함께 저장한다. controller 재시작 뒤에도 보낸 명령과 준비 중인 워커를 계산에 포함한다. 시한을 넘긴 미등록 시작 요청은 새 명령으로 대체한다.
- `pool set --expect-revision R`은 현재 revision이 다르면 충돌을 반환한다. 같은 설정의 재적용은 revision을 유지한다. `min_workers=0,max_workers=0`은 새 배정을 중지한다. 노드 상한이나 전역 실행 상한을 초과하는 최소값은 경고와 용량 부족 사유를 표시한다.
- 상한을 낮추면 먼저 유휴 워커를 종료한다. 실행 중인 워커는 새 배정을 받지 않고 현재 작업의 완료·정리를 보고한 후 나간다. `over_target_draining`은 실제 busy 수가 상한을 초과하는 동안 유지한다. `pool list`의 `busy_workers`에는 아직 실행 중인 drain 대상도 포함된다. `live_workers`는 조정 후 유지할 워커 수이며, 시작 요청과 종료 대기는 각각 `pending_starts`와 `draining_workers`에서 확인한다.
- 모든 호환 pool이 정지했으면 run의 대기 사유는 `pool_paused`, 프로젝트 동시 실행 상한이면 `project_limit`, 호환 자원이 부족하면 `capacity_full`이다.

## 검증 경로

`python3 scripts/test_postgres.py --nodes --scheduler`는 임시 PostgreSQL에 migration을 적용하고 Go race 시험을 실행한다. scheduler 시험에는 별도 DB와 로컬 소스 디렉터리를 사용한다. `scripts/scheduler_smoke.py`가 제품 controller, 두 자동 모드 에이전트, 이들이 시작한 워커를 실행한다. 시험용 handler는 Native 자식과 손자를 유지해 관측 가능한 부하를 제공한다.

| 시험 | 검증 방법 |
|---|---|
| TC-71 | 두 노드가 워커 0개로 대기하다가 서로 다른 프로젝트 두 개를 각각 실행하고, 작업 종료 후 다시 0개가 된다. |
| TC-72 | 작업 세 개가 실행 중인 풀의 상한을 1로 내린다. 두 drain 명령과 대기 사유를 확인하고 사용자 취소 전까지 세 작업의 lease와 실행이 유지되는지 검사한다. |
| TC-73 | 실제 DB에서 전역·프로젝트·pool·노드·CPU·RAM·disk 예산, 접근·권한·snapshot 조건, 프로젝트 순환 배정을 검사한다. |
| TC-74 | 실제 agent를 강제 종료한 뒤 같은 상태 디렉터리에서 재시작하고, controller도 같은 DB에서 재시작한다. 시작 명령 수와 실제 워커 수를 확인한다. |
| TC-75 | 작업 중인 agent를 정지해 lease를 만료시킨다. 재개 후 늦은 완료 요청 409, 단일 attempt, 정리 확인 후 예약 해제를 검사한다. |

migration 0004는 기존 등록 노드를 고정 모드로 유지하고 pool마다 기본 revision과 조정 상태를 추가한다. 0003에 데이터가 있는 DB의 이행·재적용·오류 rollback·구버전 거부 시험은 `migrations/w07_integration_test.go`에 있다.

W08 제품 워커도 별도 소스 root에서 `--go`와 `--autoscale`로 기동해 실제 printf 진단의 위치와 산출물 해시를 확인한다. W11의 모델 전송 승인은 각 로컬 root별로 적용한다. CLI 시험은 등록되지 않은 root를 거부하고 명시적으로 등록한 root에서 다음 등록 단계로 진행하는지 검사한다.

2026-11-18 WSL2 Linux 6.18, Go 1.27.4, PostgreSQL 17.10에서 `python3 scripts/test_postgres.py --web --nodes --scheduler`가 종료 코드 0을 반환했다. race 시험이 있는 Go 패키지 19개, 실제 Chromium 시험 1개가 통과했다. 두 자동 모드 노드의 최초 능력 광고부터 lease 만료 시험 종료까지 136초가 걸렸다. TC-71에서 시작과 종료 명령은 각각 두 개였다.

## 검증 범위

실프로세스 시험은 Linux/WSL2 한 호스트의 복수 에이전트에서 수행한다. Windows 실행 파일의 CLI 인수와 모델 소스 승인 검사는 WSL interop으로 기동해 확인했다. Windows 전체 자동 증감 운영 시험은 미실행이다. 별도 물리 호스트 사이의 네트워크 분할, RHEL 실행과 장애 시 노드 디스크 손실은 검증 범위 밖이다. 불확실한 정리는 W06의 `recovery_required`와 예약을 유지한다. 자원 예약은 입장 제어에 사용하며 OS hard limit을 보증하지 않는다. 모델 endpoint 동시 실행 제한·예약 run과 pool 관리 웹/API는 별도 구현 범위다.
