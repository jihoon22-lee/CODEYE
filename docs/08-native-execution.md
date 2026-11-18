# 08. Native 실행기

R0에서 검증한 프로세스 수명·취소·환경 격리의 범위는 [실행기 기록](26-w05-native.md)에 있다. 아래 설치·빌드·프로필별 강제 자원 제한은 대상 OS에서 별도 검증이 필요한 설계다.

## 1. 지원 목표

Native 실행은 기본 제품 경로다. Windows 11과 RHEL 8.10에서 컨테이너 없이 분석·선택적 빌드·기존 테스트가 가능하도록 구현한다. 특정 분석 도구가 해당 OS를 지원하지 않으면 그 도구만 unsupported로 표시한다. Windows 사용자에게 WSL이나 Docker를 무조건 요구하는 것으로 Native 목표를 대체하지 않는다. WSL은 별도의 Linux 노드로 등록할 수 있는 옵션이다.

## 2. 신뢰와 실행 정책

프로젝트별로 읽기·관리된 검사기 실행·프로젝트 설정/플러그인 실행·설치 스크립트·빌드·기존 테스트 권한을 저장한다. 자기 프로젝트를 한 번 승인한 후 매 실행마다 반복 입력을 요구하지 않도록 revision 기반 정책을 재사용한다. PR에서 설정 파일을 바꾸어 이 권한을 올릴 수는 없다.

> 구현 상태: 이 프로젝트 단위·revision 기반 권한 저장소는 **구현하지 않았다**. W19부터는 노드 운영자 설정이 대신한다. 저장소 코드를 실행하는 분석 작업은 노드 운영자가 local source root마다 준 허가(`--repo-code-source-root`)가 있을 때만 실행한다. 허가가 없으면 `unsupported`(`repository_code_not_allowed`)로 끝난다. Git 소스는 허가를 받지 않는다([분석기 팩 계약 §2.7](39-r1-analyzer-packs.md#27-저장소-코드-실행-허가), 결정 A23).

정적 분석도 JS 설정, build script, compiler plugin, proc macro 등을 실행할 수 있다. '정적'이라는 이름만으로 무실행이라고 분류하지 않는다. Native에서 실행되는 자식은 실행 계정 권한을 가지므로 디렉터리 복사·venv·env allowlist는 보안 샌드박스가 아니다. 신뢰 경계가 다른 프로젝트는 별도 계정·노드 또는 선택 Container/VM을 사용한다.

## 3. 실행 계약

Executor는 task ID·attempt epoch·executable·argv·cwd·env allowlist·stdin·limits·output contract를 받는다. 실행 바이너리 경로와 버전을 기록한다. PATH에 있는 동명 프로그램을 무조건 신뢰하지 않는다. 파일명과 사용자 입력을 shell 문자열로 결합하지 않는다.

필요한 shell은 명시적인 셸 프로파일로 지정한다. Linux는 승인된 bash/sh, Windows는 승인된 PowerShell 또는 Git Bash 등 프로젝트 요구에 맞춘다. 사용자 로그인 셸(csh 등)을 자동 상속하지 않는다. 직접 실행 가능한 검사기는 셸 없이 실행한다. 명령 기록에는 secret 값을 마스킹한다.

## 4. 환경과 파일

각 task는 temp/home/cache/build 경로를 명시적으로 받는다. 공유 cache는 toolchain·OS·lock digest·권한 namespace로 구분하고 lock을 적용한다. readonly 입력을 요구하는 도구에 autofix를 활성화하지 않는다. generated artifact와 source drift를 기록한다.

기본 env에는 최소 시스템 경로, 언어·locale, 명시적 미러/CA, task 전용 temp만 포함한다. 운영 DB URL·사용자 SSH agent·클라우드 토큰·Git 댓글 토큰을 전달하지 않는다. Native의 OS 파일 접근으로 원래 계정 비밀에 접근할 가능성까지 env 제거로 해결했다고 하지 않는다.

## 5. Windows 프로세스 제어

Job Object를 실행 단위로 만들고 적절한 종료·메모리·CPU 제어 가능성을 확인한다. 자식 프로세스를 제어 단위에 넣기 전 실행되는 race를 피하도록 suspended 생성→Job 연결→resume 방식을 검토한다. KILL_ON_JOB_CLOSE와 breakaway 정책을 명시하고 서비스 종료 시 자식 종료를 시험한다.

Job 연결 실패·이미 존재하는 job 제한·자식 이탈 가능성은 capability degradation으로 보고한다. PowerShell 종료만 확인하고 손자 빌드 프로세스가 끝났다고 처리하지 않는다. OS 접근 권한 격리는 Job Object만으로 제공되지 않는다.

## 6. Linux 프로세스 제어

새 process group/session을 만들고 정상 종료 요청→grace period→강제 종료→wait/reap 순서로 처리한다. 사용 가능한 cgroup v2/systemd scope가 있으면 CPU·memory·pids 제한을 적용한다. RHEL 설정과 권한에 따라 cgroup 기능은 달라지므로 probe한다. root 권한이나 호스트 cgroup 전환을 자동 요구하지 않는다.

process group만으로 탈출한 daemon까지 항상 종료된다고 보장하지 않는다. 노드 에이전트가 프로세스 정체성(PID+시작 시각)과 소유 작업을 추적하고 종료 불확실 시 recovery_required로 남긴다. 공유 서버에서 다른 사용자의 프로세스를 종료해서는 안 된다.

## 7. 자원 한도와 admission control

| 종류 | 의미 |
|---|---|
| reservation | scheduler가 할당한 CPU·메모리·동시 작업 예산 |
| enforced_limit | OS가 실제로 적용한 한도 |
| observed_usage | 수집한 실제 사용량 |
| estimated_requirement | 계획 단계의 예상 필요량 |

이 네 값은 구분한다. hard memory limit가 없으면 초과를 관찰해 중지할 수 있으나 OOM을 완전히 예방한다고 주장하지 않는다. 공유 머신에는 운영자 reserve를 두고 low priority/nice를 선택할 수 있게 한다. make/ninja/cargo/test의 내부 병렬도도 attempt 예산으로 제한한다.

stdout/stderr는 byte cap과 spool quota를 적용하고 잘림 상태를 기록한다. 원시 출력에 비밀값이 있을 수 있으므로 저장 전 마스킹한다. 결과 parser가 필요로 하는 artifact가 잘렸으면 파싱 성공으로 처리하지 않는다.

## 8. 취소와 재시작

취소는 desired state다. controller가 cancel_requested를 저장하고 agent가 모델 호출과 task 시작을 중단한 뒤 실행 그룹을 종료한다. 종료 확인 후 cancelled로 보고한다. 확인이 없으면 recovery_required다. 에이전트 재시작 시 로컬 journal과 살아 있는 프로세스를 대조한다. PID 재사용을 고려하지 않은 kill을 금지한다.

wall clock timeout, 출력 무응답 감지, 연결 상실은 다른 이벤트다. 조용한 컴파일을 로그가 없다는 이유로 실패시키지 않는다. 프로세스·호출 상태와 설정된 timeout을 함께 판단한다.

## 9. 선택 Container 실행기

동일 Executor 계약에 이미지 digest·mount·network·user·runtime 필드를 추가한다. 모든 실행을 Container로 옮겨야만 플랫폼을 사용할 수 있게 만들지 않는다. 컨테이너에서도 원본 쓰기 mount·운영 secret·무제한 자원은 기본 허용하지 않는다. 컨테이너 엔진의 존재가 네트워크 격리와 자원 제한의 적용을 보장하지 않으므로 실제 능력을 보고한다.
