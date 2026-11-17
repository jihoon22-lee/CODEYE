# 09. 워커·스케줄러·자동 증감

## 1. 객체와 범위

노드는 실제 PC/서버, 에이전트는 상주 관리자, 워커는 한 번에 한 run attempt를 실행하는 프로세스다. 초기 자동 증감은 등록된 노드 안에서 워커를 만들고 줄이는 기능이다. VM 생성·삭제는 별도 CapacityProvider 확장점이며 초기 제공 범위가 아니다.

초기 노드는 한 pool에 속한다. pool은 OS·도구·신뢰 수준·소스 접근 특성이 같은 용량 묶음이다. 에이전트는 controller에 outbound API 연결을 만든다. SSH는 사용하지 않는다. 워커는 DB나 전체 프로젝트 자격증명에 접근하지 않는다.

## 2. 초기 설정값

| 설정 | 기본 예시 | 의미 |
|---|---|---|
| min_workers | 0 | 유지하려는 프로세스 수 하한 |
| max_workers | 2 | starting/idle/busy/draining 워커 수의 목표 상한 |
| global_max_running | 2 | 전체 동시에 실행 중인 attempt 수 상한 |
| reconcile_seconds | 5 | 증감 판단 주기 |
| idle_timeout_seconds | 180 | 유휴 워커 축소 대기 |
| scale_up_cooldown_seconds | 10 | 급격한 생성 반복 완화 |
| scale_down_cooldown_seconds | 60 | 잦은 종료/재생성 완화 |
| heartbeat_seconds | 5 | 노드·attempt 생존 갱신 |
| lease_seconds | 30 | attempt 소유권 유효 기간 |
| model_max_inflight | 2 | endpoint별 동시 모델 요청 |

운영자가 UI/API에서 조정한다. 수치는 성능 실측값이 아닌 시작점이다. min<=max, nonnegative, heartbeat<lease 조건을 검증한다. 설정 수정은 revision으로 기록한다. 여러 pool의 최소값이 전역·노드 용량을 초과하면 저장 전 경고하고 degraded_capacity를 표시한다.

## 3. 노드 능력

에이전트는 OS/arch, 도구·toolchain, source-root 접근, 실행 계정/신뢰 범주, CPU/RAM·disk 여유, 예약된 용량, 적용 가능한 hard limit, OpenCode workspace 능력을 보고한다. 프로젝트 secret 전체를 heartbeat에 넣지 않는다. heartbeat가 오래되거나 disk quota가 부족하면 새 배정을 중단한다.

node.max_workers와 resource reserve는 운영자가 설정한다. 호스트 관측 여유와 정책 예산을 둘 다 만족해야 배정한다. 현재 프로세스 사용량을 이미 차감한 OS free memory와 예약량을 무조건 다시 더 빼는 방식으로 이중 계산하지 않는다. 미사용 예약과 실제 사용량의 회계 모델을 구분해 구현한다.

## 4. 자동 증감 알고리즘

```text
각 pool에 대해 단일 controller lease/epoch 획득
B = 현재 busy 워커 수
Q = 권한·도구·입력 접근 조건이 맞고 실행 가능한 대기 attempt 수
I = 현재 idle/starting 워커 수
D = clamp(B + Q, min_workers, max_workers)
노드 resource budget, global_running limit, warm-worker overhead로 D를 제한
D > current이면 호환 노드에 시작 명령 발행
D < current이면 충분히 유휴한 워커부터 drain 후 종료
busy 워커를 줄여야 하면 새 할당 금지 후 완료 시 종료
```

단순 B+Q는 초기 기준이다. 모델 endpoint가 포화되어도 CPU 검사가 가능한 작업은 진행할 수 있으므로 모든 Q를 0으로 만들지 않는다. 반대로 모델 대기 중인 run을 많이 늘려 메모리만 점유하지 않게 대기 상태·예산·단계별 수요를 고려한다. 자동 증감은 모델 서비스의 실제 처리 용량을 늘리지 않는다.

min_workers는 자원이 없어도 강제로 시작하라는 명령이 아니다. 목표를 못 채우면 원인을 표시한다. max를 busy 수 아래로 내리면 현재 작업은 살려두고 over_target_draining을 표시한다. max=0은 min=0일 때만 허용하며 새 배정을 멈춘다. 긴급 kill은 별도 명시적 취소다.

## 5. 배정과 공정성

프로젝트별 기본 동시 실행 상한 1, 전역 상한·pool·node·LLM 한도를 각각 둔다. 우선순위 안에서는 프로젝트별 round-robin과 대기 시간 aging을 적용한다. 대형 작업이 영원히 밀리지 않도록 resource-fit 실패 횟수와 대기 이유를 노출하고 예약 슬롯을 고려한다. PR이 항상 예약 작업을 굶기는 정책을 기본으로 하지 않는다.

DB claim은 짧은 트랜잭션에서 호환 run 선택, attempt 생성, capacity 예약, epoch 증가, lease 설정, 상태 이벤트 삽입을 함께 수행한다. 프로세스 실행 중 DB 행 잠금을 유지하지 않는다. 시작 명령에는 command_id가 있으며 agent는 중복 수신해도 한 번만 적용한다.

## 6. lease와 fencing

attempt에는 node_id, worker_id, lease_epoch, expires_at, status가 있다. 모든 상태·artifact finalize·finding 게시 요청에 attempt ID와 epoch를 포함한다. 서버는 현재 소유권·허용 상태·만료 여부를 검사해 오래된 요청을 409로 거부한다. raw stale artifact는 격리할 수 있으나 현재 보고서에 합치지 않는다.

agent는 마지막 유효 응답 후 monotonic 경과 시간을 추적한다. lease가 만료되기 전 보수적 안전 여유를 두고 새 task 시작을 멈추고 자식 종료를 시도한다. 노드 wall clock만 믿지 않는다. 서버는 DB 시각으로 만료를 판단한다.

네트워크 분할에서 이전 프로세스가 실제로 살아 있을 수 있다. fencing은 데이터 게시를 막는 것이지 OS 실행을 멈추는 것이 아니다. 읽기 전용 검사도 중복 자원 비용이 생길 수 있다. 부작용 가능한 build/test는 종료 증거가 없으면 recovery_required에서 운영자 판단 또는 노드 복귀를 기다린다. unknown 작업을 무조건 즉시 재실행하지 않는다.

## 7. 장애 복구와 재시도

| 실패 | 처리 |
|---|---|
| 일시적 fetch/모델 오류 | 같은 attempt 안의 제한 task 재시도 |
| 워커 사망, agent 생존 | 자식 정리 확인 후 새 attempt; snapshot 유지 |
| agent/노드 상실 | lease 만료, fenced, recovery_required |
| controller 재시작 | DB로 run/lease/capacity 재조정, 프로세스 시작 중복 방지 |
| 업로드 중단 | hash·offset으로 재전송, finalize 전 비공개 |
| pool capacity 부족 | queued+reason, 요청 실패로 오해시키지 않음 |

최종 결과 게시에는 `(run_id, attempt_id, report_revision)` 유일성을 둔다. 같은 완료 메시지 재전송은 동일 응답을 반환한다. 이전 attempt가 늦게 도착해도 새 attempt를 덮지 않는다.

## 8. 예약

IANA timezone과 cron 표현을 저장하고 UTC occurrence를 materialize한다. `(schedule_id, scheduled_for)` 유일 키로 중복 생성을 막는다. 기본 overlap은 coalesce_latest: 실행 중 새 예약이 여러 번 오면 최신 미처리 1개만 남긴다. 사용자 선택으로 skip 또는 queue를 제공한다.

서버 정지 중 놓친 예약은 기본 latest_once이며 무제한 catch-up하지 않는다. DST가 있는 시간대는 중복·누락 시각 정책을 명시한다. Asia/Seoul도 timezone 문자열을 저장한다. 예약 run의 브랜치 SHA는 실제 acquire에서 확정한다. 코드가 같아도 규칙·지침이 바뀌면 분석할 수 있다.

## 9. 수용 조건

0→N→0, idle 종료, busy drain, 최대값 축소, 두 노드 경쟁, 오래된 heartbeat, 429 포화, 같은 작업 중복 claim, lease 만료 후 late result, controller 재시작, PID 재사용, disk 부족을 시험한다. 단일 머신 성공만으로 분산 복구를 완료 처리하지 않는다.
