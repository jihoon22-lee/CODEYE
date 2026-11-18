# 25. W04 소스 확보와 독립 작업 사본

W04에서 구현한 SourceAdapter의 고정 입력과 별도 작업 사본 계약이다. 제품 워커를 통한 공개 Git·로컬 사본 검증은 [R0 파일럿](36-r0-pilot.md)에 기록했다.

## 1. 범위와 호출 계약

`internal/worker/source`는 `AcquireLocal`, `AcquireGit`, `Load`, `CloneWorkspace`를 제공한다. 입력은 승인 root/상대 경로 또는 승인된 Git host/브랜치, 플랫폼 data root, 파일·용량·재시도 한도다. 결과는 snapshot 디렉터리, 별도 writable workspace, manifest, 입력 digest다. 실제 snapshot ID와 프로젝트/attempt 소유권 연결은 control plane이 담당한다.

worker는 W03의 사용자/source-root ACL과 W06의 node/attempt 권한이 확인된 설정으로만 호출해야 한다. 라이브러리의 파일 읽기 성공이 사용자 권한 확인을 대신하지 않는다. 이 API는 shell 문자열을 받지 않는다.

## 2. 로컬/NFS

승인 root와 선택 경로를 실제 경로로 해석해 root 밖 링크·절대경로·상위 이동을 거부한다. 이 검사는 친절한 오류용이며 신뢰하지 않는다. 실제 열기는 승인 root를 `os.Root`로 연 뒤 같은 root 안에서 상대 경로를 다시 여는(`Root.OpenRoot`) 방식이라, 검증 뒤에 경로 구성 요소가 root 밖을 가리키는 symlink로 바뀌어도 밖으로 나갈 수 없고 `invalid_source`로 끝난다. 파일 열기·순회도 같은 경계에서 수행하며 symlink·special file은 따라가지 않는다.

플랫폼 data root는 **승인 root 전체의 바깥**이어야 한다(선택한 프로젝트 하위만이 아니라 승인 root의 형제·하위·상위·root 자체 모두 거부). 기존 부모의 symlink를 실제 경로로 풀어 비교하므로 별칭으로 원본 쪽에 쓰지 못한다. 승인 root와 data root가 상대 경로이면 거부한다.

파일은 Linux에서 `O_NONBLOCK`으로 연다. 스캔 뒤 일반 파일이 FIFO로 바뀌어도 open이 쓰기 측을 기다리며 멈추지 않고, 열린 뒤 종류 검사에서 변경으로 판정되어 재시도하거나 `source_unstable`이 된다. 다른 OS는 이 보호를 주장하지 않는다. 읽기는 64 KiB 단위로 context를 확인한다. 이미 시작한 단일 `read(2)`(예: 응답하지 않는 NFS의 hard mount)는 이 라이브러리가 중단할 수 없다. 그 범위를 제한하려면 executor의 subprocess 조율(W05/W06)이 필요하며, 그 전에는 이 한계를 성공이나 timeout 보장으로 표시하지 않는다.

복사 전 파일 집합·종류·크기·mode·mtime·identity를 기록하고, 파일 읽기 전후 및 전체 복사 후에 다시 비교한다. 변화가 감지되면 기본 2회 재시도하고 계속 바뀌면 source_unstable이다. NFS stat·read는 원자적 snapshot이 아니며 동일 크기/mtime로 되돌린 모든 동시 수정을 입증하지 않는다. consistency는 항상 stable_copy_best_effort다.

기본 제외 정책(이름 기반이며 비밀정보 검사가 아니다. 비밀정보 탐지는 W21):
- 어느 깊이에서든: `.git`, `.codeye`, `node_modules`, `__pycache__`.
- 선택한 트리의 **최상위에서만**: `build`, `dist`, `target`, `venv`, `.venv`. `internal/build`, `src/dist`, `cmd/target` 같은 소스 패키지는 유지한다. 하위의 `venv`/`.venv`는 그 안에 `pyvenv.cfg`가 있을 때만 제외한다(로컬은 파일 확인, Git은 트리 경로 확인).
- credential 디렉터리: `.ssh`, `.aws`, `.gnupg`, `.docker`, `.kube`, `.azure`, `.gcloud`.
- 비밀 파일 이름: `.env`, `.env.*`(단 `.env.example`/`.env.sample` 제외), `*.pem`, `*.key`, `id_rsa`/`id_dsa`/`id_ecdsa`/`id_ed25519`, `.npmrc`, `.yarnrc(.yml)`, `.netrc`/`_netrc`, `.git-credentials`, `.pypirc`, `.pgpass`, `.htpasswd`, `.dockercfg`, `kubeconfig`, `.vault_pass`, 파일 `credentials`·`credentials.{json,yml,yaml,toml,ini}`, `secrets.{json,yml,yaml,toml,ini}`, `service-account*.json`, `*.p12/.pfx/.pkcs12/.jks/.keystore/.kdbx/.ppk`, `*.tfstate(.backup)`, `*.tfvars`. `credentials`라는 이름의 디렉터리(예: SDK 패키지)는 유지한다.

제외 경로와 이유를 manifest에 남긴다. 이름이 정책에 걸리지 않는 비밀 파일은 복사되며 이 정책이 그것을 막는다고 주장하지 않는다. 생성 헤더/별도 toolchain 입력 예외 정책 UI는 후속이며 현재 고정 기본 정책을 자동 확대하지 않는다.

## 3. Git

운영자가 승인한 HTTPS host와 branch만 받아 실제 fetch 시점의 commit SHA를 고정한다. manifest에는 요청한 branch(`source_branch`)와 요청 시각(`requested_at`)을 남기며 URL·remote 이름·credential은 넣지 않는다. provider repository ID는 W03/W06이 프로젝트·attempt 소유 정보로 추적하며 이 manifest에는 아직 없다. URL userinfo·query/fragment, SSH·git://·알려지지 않은 host와 redirect는 허용하지 않는다. 현재 public HTTPS와 별도로 승인한 로컬 Git root(`AllowedLocalRoot`)를 제공한다. 로컬 Git root가 설정되면 data root는 그 root 전체의 바깥이어야 하며(HTTPS 요청도 동일) 상대 경로는 거부한다. private Git credential 전달·사내 CA profile·submodule/LFS 객체 다운로드는 미구현이며 조용히 사용자 credential이나 인터넷 대체 경로로 우회하지 않는다.

임시 bare 저장소에서 fetch→rev-parse→ls-tree→cat-file을 argv로 실행한다. snapshot/workspace에 `.git`을 넣지 않으며 checkout·hooks·smudge/clean filter·export-ignore를 사용하지 않는다. Git 사용자/system 설정과 credential helper를 차단한다. 받은 객체는 `transfer.fsckObjects`/`fetch.fsckObjects`로 검사해 `.git` 항목 같은 변조 트리를 fetch 단계에서 거부한다(정상 실패, recovery 아님). 이후 브랜치 이동은 이미 확보한 snapshot을 바꾸지 않는다.

submodule은 미확보 항목, LFS는 pointer 파일과 공백으로 기록한다. pointer bytes를 실제 대형 파일 내용으로 분석했다고 표시하지 않는다. 환경 manifest·리포트가 이 limitation을 이어받아야 한다.

`GitOptions.Timeout`은 개별 명령이 아니라 **확보 전체의 deadline**(최대 10분)이며 모든 git 명령과 객체 추출·manifest 작성이 함께 쓴다. 명령별 출력 byte 상한도 적용한다. fetch 중에는 임시 저장소의 쓰기 용량을 0.2초 간격으로 확인해 `MaxTotalBytes`+25%+1 MiB를 넘으면 process group을 중지한다. 이는 **soft 감시**이며 파일시스템 quota가 아니다. 빠른 전송은 중지 전에 한도를 초과해 쓸 수 있고, 이 감시는 fetch 이후의 정확한 객체 크기 검사를 대신하지 않는다. 취소/timeout/용량 중지에서 정리 완료를 확정하지 않고 recovery_required와 격리 디렉터리를 반환한다. 용량 중지는 `source_limit_exceeded`와 `recovery_required`를 함께 만족한다(`errors.Is` 둘 다 참). Linux는 명령 process group을 중지하지만 W05의 전체 NativeExecutor 계약을 대신하지 않는다. 다른 OS의 Git 자식 트리 종료 보증과 NFS의 중단 불가능한 kernel I/O timeout은 미검증이다. recovery 디렉터리는 자동 재사용·삭제하지 않는다.

## 4. 보존과 재시도

[manifest Schema](../schemas/snapshot-manifest.schema.json)는 상대 경로·종류·크기 또는 null·hash·mode·encoding·제외 이유·source SHA·(Git) branch와 요청 시각·고정 시각을 정의한다. [로컬 예시](../examples/snapshot-manifest.json)와 [Git 예시](../examples/snapshot-manifest-git.json)는 합성 값만 사용한다. 실제 원본 절대 경로·clone credential은 manifest에 없다.

`manifest.json`은 전체 manifest의 canonical JSON(키 정렬·공백 없음)이다. **입력 digest는 저장된 그 manifest bytes의 SHA-256**이므로 확보 시각(`source_fixed_at`)·`requested_at`·`limitations`·모든 entry를 포함한다. 같은 bytes를 새로 확보하면 시각이 달라 digest도 새로 생기며(파일 hash는 같다), 보존한 snapshot의 재시도·재복사는 같은 digest를 유지한다. 별도 입력 캐시 digest는 W04에서 두지 않는다. 머신의 절대 경로는 manifest에 없다.

`Load`는 control plane에 저장한 digest와 파일 bytes를 대조하고, canonical 형태가 아니거나 알 수 없는 필드·잘못된 종류·정렬 위반·schema 위반(예: git인데 branch 없음)이면 `snapshot_integrity_failed`로 거부한다. 따라서 limitations·시각·entry 중 하나만 바꿔도 탐지한다. `CloneWorkspace`는 각 retained 파일의 bytes/hash를 다시 확인하고 목적지의 실제 경로를 해석해 관리되는 data root 밖이거나 원본 쪽이면 거부한다. 누락/변조한 snapshot은 다른 원본으로 대체하지 않고 실패한다.

snapshot과 workspace는 byte copy이며 hard link를 공유하지 않는다. workspace 수정은 원본·snapshot을 바꾸지 않는다. 파일 readonly는 관리 정책이지 같은 OS 계정의 악성 코드를 격리하는 샌드박스가 아니다. snapshot 변조 여부는 별도 hash 검사로 판단한다.

기본 한도는 manifest 항목 50,000개, manifest JSON 16 MiB, 파일 16 MiB, 전체 512 MiB다. 재시도 workspace는 동일한 승인 data root 아래의 새 경로만 허용한다. 값을 초과한 파일을 성공한 분석 범위에 몰래 포함시키지 않는다. 실패를 0개 파일 검사 성공으로 바꾸지 않는다. 대소문자 충돌·Windows 예약명·잘못된 UTF-8 경로는 플랫폼 이동 과정에서 덮어쓰지 않고 unsupported다.

## 5. 검증과 제한

실제 실행한 시험(Ubuntu/WSL2 Linux 6.18, Go 1.27.4, git 2.53.0, `go test -race ./internal/worker/source/`, 26개 시험 통과, `go vet`은 linux/windows/darwin 대상 통과):
- 로컬: 변경 감지·재시도·계속 변경 시 `source_unstable`, symlink·special file 미복사, 승인 root 밖 링크/`..` 거부, 검증 뒤 선택 경로를 외부 symlink로 바꾸는 경쟁에서 외부 파일 미복사, 스캔 뒤 일반 파일을 FIFO로 바꿔도 멈추지 않음(`O_NONBLOCK`; 이 옵션을 끄면 시험이 timeout으로 실패함을 확인), data root가 승인 root의 형제·하위·상위·별칭·상대 경로이면 거부, 원본/snapshot/workspace 독립성, retained snapshot 재복사·변조 거부, 한도 초과·취소.
- 제외 정책: 흔한 credential 파일 이름 제외와 `internal/credentials`·`.env.example` 등 소스 유지, `internal/build`·`src/dist`·`cmd/target` 유지, 최상위 `build`·`dist`·`target`·`venv` 제외, `pyvenv.cfg`가 있는 하위 venv 제외, 어디서든 `.git`·`node_modules`·`__pycache__` 제외. Git도 같은 정책.
- Git(실제 fixture): 대기 중 HEAD 변경·확보 이후 변경 무영향·export-ignore/filter 미적용·LFS gap·`.git` 제거, branch/요청 시각 기록과 manifest에 URL·절대경로 없음, `.git` 항목을 가진 변조 트리를 fsck로 거부(정상 실패), 확보 전체 deadline(각 명령은 빠르나 합산이 초과하면 `recovery_required`), fetch 용량 soft 감시가 폭주하는 fetch를 중지해 `source_limit_exceeded`+`recovery_required`와 격리 디렉터리를 남김, 한도 내 fetch는 건드리지 않음. 용량·deadline 시험은 가짜 git 실행 파일을 사용한다.
- manifest: digest가 `manifest.json` bytes의 SHA-256이고, limitations·시각·entry·알 수 없는 필드·비-canonical 형식 변경을 모두 `snapshot_integrity_failed`로 거부, 보존한 snapshot의 재시도는 같은 digest, 새 확보는 새 digest. 두 예시와 실제 확보 manifest의 키가 schema와 일치. 별도로 Python `jsonschema`로 두 예시 통과와 branch/requested_at 누락·로컬에 branch 지정·알 수 없는 키 거부를 확인했다.

한계(검증하지 않았거나 보장하지 않음):
- NFS/응답하지 않는 파일시스템의 이미 시작한 `read(2)`·`open(2)`는 중단할 수 없다. context는 chunk 사이에서만 확인한다. 실제 상한은 executor subprocess 조율(W05/W06) 전에는 없다.
- Git fetch 용량 감시는 polling 기반 soft 한도이며 filesystem quota가 아니다. 감시 간격 사이의 빠른 전송은 한도를 넘겨 쓸 수 있다.
- `O_NONBLOCK` FIFO 보호와 Git process group 중지는 Linux에서만 시험했다. Windows junction/UNC 경계, macOS는 시험하지 않았다. 다른 OS의 Git 자식 트리 종료는 미검증이다.
- 이름 기반 제외는 비밀정보 검사가 아니다. 정책에 없는 이름의 비밀 파일은 복사된다. mtime 위·변조는 시간·크기 비교로 입증할 수 없으며 consistency는 `stable_copy_best_effort`다.
- private Git credential, 사내 CA·proxy, GHES 실기, submodule/LFS 객체 다운로드는 구현하지 않았다(환경 변수를 비워 사용자 설정이 새어 들지 않게 한다). provider repository ID는 manifest에 없다.
- private Git/GHES/NFS 실기, Windows 11/RHEL 실기, 온라인 대규모 저장소, 실제 W06 배정→W08 분석→W09 보고서 경로는 아직 이 단계의 검증이 아니다. REQ-003/015와 TC-03/04의 소스 확보 부분을 구현하며 ACL·프로세스·보고서 전체 완료를 주장하지 않는다.
