# 변경 이력

CODEYE가 얻은 기능을 최신순으로 적는다. 항목 끝의 `#NN`은 병합된 풀 리퀘스트 번호다. 현재 동작과 계약은 각 주제 문서(`docs/`)가 기준이며, 이 파일은 무엇이 언제 더해졌고 무엇을 어떤 환경에서 확인했는지만 기록한다.

## 진행 중

### 추가 — 한 서비스, 사용자별 허가

- **다른 머신에 서비스**: `codeye up --listen ADDR --plain-http`(경고 한 줄과 함께 평문 HTTP) 또는 `--tls-cert`·`--tls-key`·`--public-origin`(TLS). 듣는 방식은 설정 파일에 저장돼 `codeye start`와 사용자 서비스도 같은 주소로 뜬다. 이 머신의 에이전트와 명령은 loopback listener(기본은 listen 포트 + 1)를 쓴다. 로그인 링크와 관리 화면의 "이 서비스 주소"는 public origin이다([다른 사람 초대](docs/GUIDE.md#다른-사람-초대), A52).
- **모드 대신 사용자별 허가**: 같은 시작 화면에서 각 사람이 분석할 수 있는 것을 허가로 정한다. 관리자는 웹에서 서버 폴더를 더하고 사용자별로 또는 모두에게 열며(`POST /admin/source-roots`, `PUT /admin/source-roots/{id}/everyone`), Git host를 승인하고(`POST /admin/providers`), 두 스위치 "다른 사용자도 모델 검토 사용"(기본 켬)·"다른 사용자의 저장소 빌드 스크립트 실행 허용"(기본 끔)을 바꾼다(`GET`·`PUT /admin/settings`). 스위치는 run을 시작한 사람 기준이고, 스위치 때문에 빠진 것만 보고서에 `model_review_not_offered`·`repository_code_not_offered`로 남는다. 허용된 폴더가 없는 사람에게는 "이 서버에서 읽도록 허용된 폴더가 없습니다. …"와 읽을 수 있는 폴더 목록을 보인다. `GET /setup`은 사용자마다 답하고 `mode` 필드가 없어졌다.
- **사용자별 GitHub token**: 계정 메뉴의 GitHub 연결(`GET`·`PUT`·`DELETE /account/github`). token은 `secrets/github-users/<user_id>.json`(0600)에 있고 응답·로그·감사 detail에 없다. `codeye up`이 찾은 token은 관리자의 연결이다(찾은 그대로 `secrets/github.json`에도 남는다). 모든 GitHub 호출은 그 호출이 대신하는 사람의 token을 쓴다(pull request 고정·Git fetch·결과 댓글은 run을 시작한 사람, 미리 보기는 요청한 사람). token이 없으면 공개 저장소만 읽고 댓글은 `no_token`으로 남는다([누구의 token인가](docs/GITHUB.md#누구의-token인가)).
- **`codeye up`의 에이전트**는 웹에서 더한 폴더와 Git host를 job에서 받는다(`--controller-sources`, `--git-credential-dir`). 따로 띄운 노드는 운영자 플래그를 상한으로 둔다.
- migration 0014(`source_root_everyone`, `service_settings`, 감사 action `source_root.everyone_set`·`service.settings_set`·`account.github_token_set`·`account.github_token_removed`). `POST /projects`의 `open_existing`(같은 폴더·저장소면 그 프로젝트를 연다).
- **고침**: `codeye up --listen ADDR`이 ADDR를 분석 대상으로 읽던 것. 시험 하나(`TestUpGitTarget`)가 실행한 사람의 실제 상태 디렉터리(`~/.local/state/codeye`)를 만들던 것. 새 설치의 관리자 표시 이름은 `admin`이다(기존 설치는 그대로).
- **확인**: 두 사용자 smoke(`scripts/test_postgres.py --web-service`, TC-263)가 loopback 별칭의 평문 HTTP와 자체 서명 TLS에서 통과했다. 이 호스트의 LAN 주소(WSL2 eth0)로 TLS와 `--plain-http`를 한 번씩 띄운 확인은 시험용 빌드와 가짜 GitHub를 쓴 스크립트였고, 브라우저와 API 호출도 같은 호스트에서 그 주소로 보냈다(다른 사용자의 로그인·시작 화면·폴더 거절·GitHub 연결·관리자 설정, 다른 Host는 403). 실제 다른 물리 머신의 브라우저와 GHES는 시험하지 않았다.

### 추가 — 여러 노드 검증과 운영 수용

- **정리 확인**: `recovery_required` run을 사람이 "노드에 남은 프로세스가 없음"을 확인한 뒤 끝낸다. API `POST /runs/{id}/recover`(`{"cleanup_confirmed": true}`), 운영자 CLI `codeye run recover --id RUN_ID --confirm-cleanup [--config PATH]`(개인 모드는 설정의 DB), 웹의 확인 상자와 단추. 한 트랜잭션에서 attempt `released`·`confirmed`, 예약 해제, run `failed`(`operator_confirmed`), 감사 `run.recovery_confirmed`이고 이어서 다시 시도처럼 대기열에 넣는다(신선한 노드가 snapshot을 가질 때만 같은 run). pull request run은 고정한 pull request(head SHA·변경 파일)와 댓글 선택을 그대로 가진 채 다시 들어간다([정리 확인](docs/API.md#정리-확인), A48, migration 13).
- **보존 정리**: `codeye cleanup --older-than 30d`가 기본 dry run이고 `--apply`일 때만 끝난 run의 오래된 원시 산출물(`stdout`·`stderr`·`raw_output`·`other`) 내용을 지운다. 행·hash·보고서·관찰·감사는 남고 run 화면에 "원시 증거가 삭제됨"이 보인다. 노드 상태 디렉터리의 끝난 작업 사본도 정리하되 에이전트가 실행 중이면(또는 확인할 수 없으면) snapshot은 건너뛴다([보존 정리](docs/OPERATIONS.md#보존-정리), A49).
- **사용자 서비스**: `codeye service install|uninstall|print`가 systemd 사용자 unit(`codeye start`/`stop`)을 쓴다. install은 enable만 하고, uninstall은 설정 없이 자기가 쓴 unit만 지운다([사용자 서비스](docs/OPERATIONS.md#사용자-서비스), A50). 세 명령 모두 네트워크를 쓰지 않아 오프라인 모드에서도 같고, 운영 훈련 D7·D9는 오프라인 모드 설치에서 실행한다.
- **고침**: drain 중인 노드가 자동 증감 계획에 남아 `start_worker`를 받고 그 노드의 예산을 시작 제한시간(90초) 동안 잡던 것. drain 중인 에이전트는 정적 scaling과 분석 능력 없음을 알린다(A51). `service uninstall`이 설정 파일을 요구하던 것, 보존 정리가 실행 중 attempt가 쓰는 snapshot을 지울 수 있던 것, 개인 모드에서 `run recover`가 DB를 찾지 못하던 것.
- **여러 노드 smoke**(`scripts/test_postgres.py --multinode`, TC-250~254, CI 작업 `native-multinode`): 한 WSL2 호스트에서 `codeye serve` 하나와 노드 신원 8개(각자 상태 디렉터리·도구·source root). 장애는 SIGSTOP/SIGKILL과 로컬 TCP 프록시 차단이다. 이 호스트에서 전체 116~125초, 모두 통과. 표는 첫 측정이고 다시 실행한 값도 그 근처였다(사망 → `recovery_required` 32~34초, 다시 빌드는 build cache에 따라 1.3~7.5초).

  | 시나리오 | 한 일 | 관찰 | 시간 |
  |---|---|---|---|
  | S1 능력 배정·대기 | go/ruff/도구 없음 노드, 늦게 오는 local root 노드와 Git host 노드 | 맞는 노드만 받음, ruff만 있는 노드의 go vet `unsupported`/`tool_not_configured`, `no_capable_node` 대기 뒤 시작 | 등록 → claim 0.2초, Git 노드 → 끝 5.8초 |
  | S2 노드 사망 | 작업 중 에이전트·워커·작업 트리 SIGKILL | stale 19초, `recovery_required` 34초, 자동 재배정 없음, 옛 epoch complete 409 + `attempt.stale_rejected`, 확인 없이 400, 확인 뒤 새 run이 다른 노드에서 끝(2.8초), 그 뒤 옛 complete도 409·결과 불변, 다시 띄운 에이전트가 정리 보고 | 34.1초 |
  | S3 연결 끊김 | 프록시로 한 노드의 연결 차단 뒤 복구, lease 안의 8초 SIGSTOP | 차단 중 다른 노드가 실행하지 않음, `lease_lost` 19초, 복구 → `failed`(`cleanup_reported`) 0.2초, 같은 노드가 다음 일을 받음, SIGSTOP은 상태를 바꾸지 않음 | 19.1초 |
  | S4 drain | 작업 중 SIGUSR1 | 하던 일 첫 attempt로 끝, 새 run `no_capable_node`, 그 노드에 `start_worker` 없음, 종료 코드 0 | 15.0초 |
  | S5 에이전트 갱신 | `-trimpath`로 다시 빌드(hash 다름), 같은 상태 디렉터리로 시작 | 같은 node ID, `node.enrolled` 감사 없음, 대기 run을 받아 끝 | 빌드 7.5초, 시작 → 끝 5.2초 |

- **운영 훈련**(`scripts/test_postgres.py --ops-drill`, TC-255): 자기 PostgreSQL cluster를 띄워 그것만 멈춘다. 이 호스트에서 약 9분이라 CI에는 넣지 않았다. 마지막 두 번 모두 통과(D9의 지금 형태는 마지막 실행).

  | 훈련 | 한 일 | 관찰 | 시간 |
  |---|---|---|---|
  | D1 모델 endpoint 실패 | OpenCode + HTTP 500만 돌려주는 가짜 upstream | probe `FAILED`(`model_timeout`), run `partial`(136초), 정적 결과 유지, 사유 `dependency_failed`·`model_timeout`이 보고서 문장에 보임 | 263초 |
  | D2 노드 offline | 작업 중 노드 트리 SIGKILL, CLI 정리 확인 | `recovery_required` 35초, `--confirm-cleanup` 없이 종료 코드 2, 있으면 `failed` + 새 run(감사 `operator_cli`), 다른 노드에서 끝 | 35초 |
  | D3 DB 짧은 중단 | 작업 중 cluster 8초 정지 | `ready`·`GET run` 503, 정지 8초 뒤 다시 ready, 같은 attempt로 끝 | 30초 |
  | D3 DB 긴 중단 | 40초 정지(lease 30초) | `lease_lost` 20초, DB 복구 4.7초 뒤 `failed`, 정합성 검사 0건, doctor `available`, 새 run 끝 | 55초 |
  | D4 도구 변경·사라짐 | ruff 버전을 올린 wrapper, 그다음 ruff 없음 | 비교에 `tool:python.ruff:version` 차이 행, 사라진 작업은 `absent_unverified`(`check_failed`)이고 `resolved_observed` 0 | 1.5초 |
  | D5 max workers 축소 | 작업 중 pool 2 → 1 | drain 0.4초, 취소 없음, 작업 끝 | 35초 |
  | D6 산출물 상한 | 상한 1 → 0 | 상한 넘으면 새 작업만 대기, 하던 일은 끝 | 5.4초 |
  | D7 실행 중 백업·복원 | 작업 중 온라인 백업 → 새 DB·상태에 복원 → 시작 | 백업 0.1~0.3초, 복원 0.2~0.4초, 같은 보고서, 실행 중이던 run `recovery_required` → `codeye run recover --config`로 확인 → 이어 실행 끝, 새 run 끝 | 24~26초 |
  | D8 권한 회수 | 구성원 제거, 사용자 비활성화, 노드 철회 | 열린 SSE가 0.5초 안에 `access_revoked`·`unauthenticated`로 닫힘, 다음 요청 404·401, 철회 4.9초 뒤 에이전트 종료 코드 4, 남은 프로세스 없음, 정리 확인 뒤 새 run | 38초 |
  | D9 보존 정리 | 끝난 run의 산출물을 40일 전으로 돌린 뒤 `codeye cleanup --config` dry run → `--apply` → dry run | dry run은 원시 행 6개(풀 내용 2개)를 보이고 바꾸지 않음, 적용 뒤 파일 1개 삭제, 1개 남김(`kept_files`: 빈 내용이거나 그 사이 다시 참조됨. 어느 쪽인지는 구분하지 않았다), 같은 보고서, 감사 `storage.retention_applied` 한 건, `raw_evidence_removed_at`, 다시 dry run 0건 | 0.1초 |

- **활성 controller는 하나다.** 두 controller를 같은 DB에 띄우는 구성은 지원하지 않고 시험하지 않았다. 여러 물리 노드, NFS, 실제 네트워크 분할, RHEL 8.10, 회사 SSO, GHES는 이 호스트에서 확인할 수 없다([로드맵](docs/ROADMAP.md#실제-환경에서만-검증할-수-있는-것)).
- 사용자 systemd(WSL2): install(enable) → start 1.3초 running, stop 0.7초(PostgreSQL도 멈춤), uninstall.
- 확인 환경: Linux(WSL2, 6.18), Go 1.27.4, PostgreSQL 17.10, Python 3.14.4. 시간은 이 호스트의 측정값이며 보장값이 아니다. 실모델 호출은 하지 않았다.

### 추가 — GitHub token, pull request 분석, 결과 댓글(개인 모드)

- **token**: `codeye up`이 `GH_TOKEN`, `GITHUB_TOKEN`, `gh auth token --hostname github.com` 순으로 github.com token을 찾아 `GET /user`로 확인하고 `<state>/secrets/github.json`(0600)에 둔다. 출력과 `GET /setup`의 `github`는 있음·계정·출처·이유만 보인다. 설정 `"github": {"enabled": false}`가 끈다. 워커는 파일 경로(`--git-credential-file`)만 받아 github.com fetch의 HTTP header로만 보낸다(git 환경변수, argv·URL·작업 사본에 없음). 비공개 github.com 저장소를 읽는다([GitHub token](docs/GITHUB.md#github-token), A46).
- **pull request 분석**: `codeye up https://github.com/OWNER/REPO/pull/N`과 화면 입력란. controller가 base/head SHA, head 저장소(fork), 변경 파일·상태·변경 줄을 run에 한 번 고정하고(`runs.pull_request`, migration 12, `POST /projects/{id}/runs`의 `pull_request: {number, comment?}`), 워커가 `refs/pull/N/head`를 받아 고정 SHA를 확인한다. snapshot manifest는 branch 대신 `source_ref`를 싣는다. 새 push 뒤 다시 주면 새 run이고 비교 기준은 같은 pull request의 이전 run이다([pull request 분석](docs/GITHUB.md#pull-request-분석)).
- **검토 순서**: pull request run은 변경 파일, 같은 폴더 파일, 나머지 순서가 A45의 분류·순위보다 앞선다. 삭제 파일은 검토하지 않는다. 범위 산출물 order `pull_request_changed_first`, 파일 label `changed`·`neighbor`. pull request가 아닌 run의 계획과 요청 바이트는 그대로다(기존 계획·요청 바이트 고정 시험 통과. 회귀 집합 dry run은 실행하지 않았다)([검토 우선순위](docs/REVIEW.md#검토-우선순위)).
- **보고서 builder 9**: `pull_request` 절이 발견·도구 진단을 변경된 줄·변경 파일·변경 파일의 다른 줄·그 밖으로 나누고(patch가 없으면 파일 단위), 요약이 pull request·SHA·모델에 보낸 변경 파일 수를 말하며 "저장소의 나머지는 같은 깊이로 검토하지 않았을 수 있다"고 쓴다([pull request 보고서](docs/REPORT.md#pull-request-보고서), [예시](examples/report-pull-request.json)).
- **결과 댓글**: 선택(기본 끔, `codeye up --comment`, 화면 "고급"의 체크, 프로젝트별 `analysis.comment_on_pull_request`). 보고서 게시 transaction의 outbox를 controller가 lease로 게시한다. token 계정의 marker 댓글 하나를 고치고, publication version으로 더 새 결과를 덮지 않으며(`superseded`), rate limit·연결 실패는 1분부터 30분까지 6번 다시 시도한다. 실패는 run의 댓글 상태(`GET /runs/{id}`의 `pull_request.comment`)로만 남는다. 본문은 구조화된 보고서에서만 만들고 코드·도구 원문·비밀정보를 싣지 않으며 `&`를 먼저 escape하고 mention·issue 참조·자동 링크를 끊는다. 미리 보기는 `codeye up --dry-run-comment`와 `GET /runs/{id}/pull-request-comment`(화면의 "미리 보기")이고 GitHub에 쓰지 않는다([결과 댓글](docs/GITHUB.md#결과-댓글)).
- **서버 모드**: 바뀌지 않는다. token을 찾지 않고 pull request 요청은 `PULL_REQUEST_UNSUPPORTED`다. 여러 사람이 쓰는 설치의 계획은 GitHub App과 webhook이고 polling은 만들지 않았다.
- **고침**: 시험 안 된 경로에서 찾은 것. pull request snapshot의 manifest에 branch가 없어 보고서가 `report_input_invalid`로 거부되던 것(`source_ref` 추가), 모델이 없는 pull request run의 요약이 "변경 파일 7개 중 0개를 모델에 보냈다"고 쓰던 것(정적 분석만 거쳤다고 쓴다), 도구 표에 gh가 더해져 묶음 검사가 위치로 고른 항목이 바뀐 것.
- 확인 환경: Linux(WSL2), Go 1.27.4, PostgreSQL 17.10, gh(snap), 임시 설정·상태·데이터 디렉터리, 모델 없음. 가짜 GitHub API(`scripts/fake_github.py`)와 Git 미러로 `--comment`(댓글 하나 게시), 새 push 뒤 같은 댓글 갱신, `--dry-run-comment`(쓰기 요청 0), token이 `secrets/github.json`에만 있음을 확인했다(`scripts/personal_smoke.py`). 실제 GitHub에서는 읽기만 했다. 비공개 저장소 하나를 gh token으로 가져와 분석했고(소스 확보 성공, 커밋 고정), 공개 `spf13/cobra` pull request #2580(fork, 변경 파일 7)을 `--dry-run-comment`로 분석해 댓글 본문을 출력했다. 두 run 뒤 상태·설정 디렉터리 1,935개 파일 중 token이 있는 파일은 `secrets/github.json` 하나였다. 가짜 모델 upstream으로 같은 저장소의 기본 브랜치(`adbc881`, pull request의 base)와 #2580을 분석하면 overview 요청의 파일 8개 중 변경 파일이 1개에서 7개 중 6개(변경된 Go 파일 전부, 나머지 1개는 문서)로 바뀌었고, 변경 파일이 slice 1~3에 흩어지던 것이 overview로 모였다. 가짜 upstream의 결과는 경로 검증이며 검토 품질의 근거가 아니다. 실제 pull request에는 댓글을 게시하지 않았다.

### 추가 — 폐쇄망 배포팩, 오프라인 모드, 사내 CA

- **배포팩**: `scripts/offline_pack.py build`가 정적 링크 실행 파일(CGO 없음, interpreter·dynamic section이 있으면 실패), `web/dist`, 사용 안내서·운영 문서, 오프라인 설정 예시, `INSTALL.md`, `MANIFEST.json`(버전·commit·빌드 시각·OS/arch·Go·Node 버전, 실행 파일에 든 migration·지침 팩의 hash, 모든 파일의 sha256), `SHA256SUMS`를 묶음 하나(linux/amd64 약 8 MB)로 만든다. 같은 commit·toolchain이면 바이트가 같다. `verify`가 바뀐·빠진·더해진 파일을 찾는다. `codeye version [--json]`을 더했다. 분석 도구·PostgreSQL·OpenCode는 싣지 않는다([배포팩](docs/OPERATIONS.md#폐쇄망-배포팩), A47).
- **웹 화면 외부 주소 검사**: `check-web`이 빌드된 화면의 외부 글꼴·CDN·`@import`·`url()`·`fetch`·원격 source map을 찾으면 실패한다. CI 웹 작업이 실행한다.
- **오프라인 모드**: `"offline": true` 또는 `codeye up --offline`. 적지 않은 공개 proxy·레지스트리·index·crates.io를 쓰지 않고(`offline_no_registry_configured`), Go는 운영자 모듈 캐시나 사내 proxy만 쓰며 checksum DB를 정하지 않았으면 끈다. `git_hosts` 기본이 빈 목록이다. 모델은 `base_url`(사내 endpoint)이 있는 키 파일·relay만 쓰고(`model_endpoint_not_configured`), 환경변수의 키를 저장하지 않으며 OpenCode `auth.json`을 찾지 않는다. OpenCode가 시작할 때 묻는 npm 레지스트리 조회는 오프라인 여부와 관계없이 loopback의 닫힌 포트로 보낸다.
- **사내 CA**: `"ca_bundle"`(합본 PEM)을 go·npm·pnpm·yarn·pip·uv·cargo·git·OpenCode에 `SSL_CERT_FILE`·`NODE_EXTRA_CA_CERTS`·`GIT_SSL_CAINFO`·`PIP_CERT`·`CARGO_HTTP_CAINFO`로 준다. 에이전트는 시작할 때 파일을 검사한다. 검증을 끄는 설정은 없다.
- **점검**: `codeye doctor --config`의 `offline` 절("Offline readiness")이 접속할 곳과 출처·받지 않는 이유, CA 상태, PostgreSQL 프로그램, 언어별 빠진 도구를 보인다. `codeye tools --checklist`가 폐쇄망용 도구 점검표(시험한 계열, 옮기는 방법)를 낸다.
- **GitHub과 함께**: 오프라인 모드에서는 GitHub token을 찾지 않고(`gh auth token` 실행 없음) 저장된 token을 지우며, pull request 주소를 거부하고 controller가 GitHub API를 부르지 않는다. `up` 출력·`GET /setup`(`github.reason: offline_mode`)·doctor(`github_api` 목적지)가 이를 말한다. git fetch는 CA 변수와 GitHub header를 함께 받는다. 점검표에 `gh`(선택, 온라인 전용)가 있다.
- **고침**: 오프라인에서 환경변수의 API 키가 키 파일을 덮어쓰던 것(배포팩 smoke가 찾았다).
- 확인 환경: Linux(WSL2, Ubuntu 26.04, glibc 2.43), Go 1.27.4, Node 24.18.0, PostgreSQL 17.10, OpenCode 1.19.5. `scripts/offline_smoke.py`가 묶음을 빈 디렉터리에 풀어 그 실행 파일만으로, 새 HOME·설정·상태로 network namespace(loopback만) 안에서 실행했다. 모듈 캐시에 있는 Go 프로젝트는 `prepared`, 없는 프로젝트는 `offline_no_registry_configured`였다. 사설 CA의 HTTPS 미러(Go proxy·PyPI simple·npm·`git http-backend`)와 TLS로 띄운 가짜 모델 upstream으로 Go·Python·JS 의존성을 미러에서 받고 Git 주소 프로젝트를 확보했으며 모델 검토가 사내 endpoint로 `performed`였다(모델 호출·외부 다운로드 없음). 모든 connect(strace)와 DNS 조회(기록기, 자체 확인 포함)를 기록했고 loopback 밖으로 나간 것은 없었다. go·git·npm·pnpm·yarn·pip·uv·OpenCode는 CA 변수가 있으면 성공하고 없으면 인증서 검증 오류였다. 실행 파일은 빈 chroot(libc 없음)에서도 뜬다. 실제 폐쇄망, RHEL 8.10, 회사 CA·미러, cargo의 CA는 확인하지 않았다.

### 바뀜 — 웹 화면 다듬기

- **시각 체계**: 색·글자·간격 토큰과 단추·입력·카드·상태 표지·표·펼침 요소의 공통 모양을 스타일시트 하나에 정했다(새 의존성 없음). 상태는 기호·글자·색을 함께 쓴다. 대비는 색 값으로 계산해 글자 4.5:1, 입력 테두리·초점 3:1 이상이다([접근성](docs/REPORT.md#접근성)).
- **첫 화면**: 폴더 경로·Git 주소 입력과 분석 단추를 맨 위에 크게 두고 프로젝트는 그 아래 카드로 보인다. 프로젝트를 고르면 최근 실행과 보고서가 바로 열린다. 프로젝트 ID·설정 버전은 "프로젝트 정보"에 접었다.
- **실행 화면**: 상태 표지, 진행 막대, 단계별 줄(상태와 이유)을 먼저, 실행 ID·스냅샷·진행률 계산 근거는 "실행 세부 정보"에 접었다. 다시 시도·취소는 보고서 앞에 있다. 마지막 변화·서버 확인 시각은 "3분 전"처럼 보이고 정확한 시각은 마우스를 올리면 보인다. 탭 제목이 실행 상태와 진행률을 따른다.
- **보고서**: 판정을 문장으로 먼저 말하고 먼저 볼 것·우선 확인할 발견을 수와 표보다 앞에 둔다. 발견 카드는 심각도·검증 상태 표지와 첫 위치를 요약에, 영향·개선 방향을 펼친 첫머리에, 주장과 근거를 별도 펼침에 둔다. 규칙별 진단 표는 규칙·메시지·수·심각도·위치 순이고, 모듈별 검토 장부 위에 보낸 범위를 한 문장으로 쓴다.
- **문구**: 내부 용어(입력 고정, 실행기, 노드 운영자, revision)를 쉬운 말로 바꾸고 원문 코드(`role_skipped`, `dependency_failed`, `tool_not_probed`, 규칙 표의 심각도 등)에 이름을 붙였다. 로그인·연결 불가·도구·"더 나은 결과를 얻으려면"의 터미널 명령에 복사 단추를 두었다. 환경 진단 화면의 내부 구현 단계 목록을 뺐다.
- 확인 환경: Linux(WSL2), Go 1.27.4, PostgreSQL 17.10, Node 24.18.0, Chromium(Playwright 1.65.1). 임시 상태 디렉터리·모델 없음으로 `codeye up`한 `fixtures/modules/input` 사본과 `spf13/cobra`(두 번 실행해 비교 포함), 저장해 둔 실제 모델 검토 보고서(cobra)를 화면에 띄워 1280px·400px 폭으로 모든 화면을 찍어 보았고, 키보드 Tab으로 첫 화면부터 보고서·예약 양식까지 70개 요소를 지나며 모두 초점이 보였다.

### 추가 — Rust·JS/TS·Python 의존성 받기

- **Rust**: `deps.rust`·`deps.rust.mNN`이 cargo check와 같은 모듈 계획으로 `cargo fetch --locked --target <host>`를 실행해 crate를 공유 `CARGO_HOME`(`rust/cargo`)에 받고, cargo check·clippy는 그것을 오프라인으로 읽는다(`dependencies_prepared`). `Cargo.lock`이 없으면 만든 lockfile을 task 디렉터리에 두고 분석기가 실행하는 동안만 사본에 놓는다. 저장소 코드 실행 허가가 없으면 받지 않는다(`dependency_consumers_not_allowed`)([Rust 받기](docs/ANALYZERS.md#rust-의존성-받기), A43).
- **JS/TS**: `deps.javascript`·`deps.javascript.mNN`이 lockfile이 있는 설치 루트마다 npm·pnpm·yarn 1을 lifecycle script 없이 실행해 이번 attempt의 작업 사본에 `node_modules`를 설치하고 기록의 `installed_dirs`에 적는다. tsc·type-aware ESLint는 적힌 실제 디렉터리만 받아들인다. 설치 위치·캐시·레지스트리·proxy를 명령줄로 정하고, pnpm의 `packageManager` 릴리스 전환을 끄며, 작업 사본 밖을 가리키는 링크를 지운다. 스냅샷 밖 `file:`·`link:`·`portal:` 의존성이 있으면 설치하지 않는다(`local_dependency_outside_project`). yarn 2 이상·bun은 받지 않는다([JS/TS 받기](docs/ANALYZERS.md#jsts-의존성-받기)).
- **Python**: mypy·pyright 모듈마다 `deps.python`·`deps.python.mNN` task가 `uv.lock`(uv `export --frozen --offline`), `pyproject.toml`의 `[project]` dependencies·optional-dependencies·dependency-groups, `requirements*.txt`를 읽어 CODEYE의 요구사항 파일을 쓰고 wheel만 트리 하나에 설치한다(uv가 있으면 `uv pip install --no-build`, 없으면 `--python`의 `pip install --only-binary=:all:`). wheel이 없는 요구사항은 빼고 다시 설치해 `partial`로 남긴다. 저장소가 적은 index·editable·로컬·VCS 요구사항은 따르지 않고 `dropped_inputs`에 남긴다. 받은 트리의 `*.pth`·`sitecustomize`는 지운다. 버전이 모두 고정되면 공유 트리(`python/trees/<key>`)를 다음 run이 다시 쓰고, 아니면 attempt 트리다([Python 의존성 받기](docs/ANALYZERS.md#python-의존성-받기), A44).
- **Python 분석기 연결**: pyright는 `venvPath`/`venv`, mypy는 `--python-executable`(운영자 interpreter와 받은 site-packages를 묶은 scratch 가짜 venv)로 받은 트리를 읽고 `third_party_packages_prepared`(`partially_prepared`)를 남긴다. Windows의 mypy는 패키지 없이 검사한다(`prepared_packages_not_used`).
- **도구와 설정**: 도구 찾기가 패키지 관리자 npm·pnpm·yarn(node 진입 스크립트)과 uv·python(pip이 없으면 uv가 있을 때만)을 찾고(`tools.json` 23개 도구), 받기 task가 실행하는 방식으로 검증한다. 에이전트 플래그 `--npm`·`--pnpm`·`--yarn`·`--uv`·`--python`, `--npm-registry`·`--pip-index-url`, 개인 모드 `agent.tools.*`와 `agent.dependencies.npm_registry`(비우면 사용자 `npm config get registry`)·`pip_index_url`(인증정보 URL 거부). 패키지 관리자는 분석기가 아니므로 hello의 `dependencies.package_managers`로 워커에 간다(`deps.ManagerFlags`). 의존성 기록에 `installed_dirs`(JS/TS, attempt 캐시)를 더했다([예시](examples/dependencies-javascript.json)).
- **안내**: "더 나은 결과를 얻으려면"이 빠진 받는 도구를 짚는다. Rust는 cargo, JS/TS는 node가 없으면 node를, 있으면 lockfile이 고른 관리자를, Python은 python 설치를 말한다. 빌드 스크립트를 켜면 crate도 받는다는 것을 알린다.
- **보고서**: 받기가 꺼진 run에서 Python 타입 검사기가 패키지 없이 import를 풀지 못하면 `dependencies_not_fetched`(`imports_unresolved`)이고 "의존성 받기 켜고 다시 시도"가 나온다. 아직 출시하지 않은 builder 7 안의 변경이라 버전은 그대로다. Python 사유·한계의 한국어 라벨을 더했다.
- **고침**: 받기 전에 건너뛴 의존성 기록(`pinned=false`)에 `dependency_versions_not_pinned` 공백이 붙던 것. 받은 기록에만 단다.
- 확인 환경(Rust·JS/TS): Linux(WSL2), Go 1.27.4, PostgreSQL 17.10, Node 24.18.0, npm 11.16.0, pnpm 11.28.13, yarn 1.22.22, cargo 1.99.1. 임시 설정·상태 디렉터리, 모델 없음으로 `codeye up`을 실행했다. 모든 보고서가 `partial`/`inconclusive`인 이유는 모델 검토가 없어서다.
  - `sharkdp/hexyl`(`6ecc29b`): 빌드 스크립트 끔이면 `deps.rust` `skipped_policy`/`dependency_consumers_not_allowed`, cargo check·clippy `unsupported`/`repository_code_not_allowed`. 빌드 스크립트 켬·받기 끔이면 cargo check·clippy `failed`/`dependency_unavailable`, 관찰 14·발견 0. 받기 켬이면 `deps.rust` 67/67, cargo check `succeeded`, clippy `succeeded`(관찰 2), 관찰 16·발견 2.
  - `pmndrs/zustand`(`9fed2eb`, pnpm): 받기 끔이면 tsc·type-aware ESLint `failed`/`dependency_unavailable`, 관찰 187·발견 141. 받기 켬이면 `deps.javascript` pnpm 11.28.13로 558/558, tsc `succeeded`(진단 0), type-aware ESLint `succeeded`(관찰 268), 관찰 455·발견 409. 루트 lockfile이 맡지만 workspace 멤버가 아닌 `examples/starter`의 tsc·ESLint(`.m02`)는 두 경우 모두 공백이다. gitleaks 처리 범위는 두 경우 모두 144 파일(설치 트리를 세지 않음)이다.
  - `developit/mitt`(`6b41670`, lockfile 없음): npm 11.16.0으로 21개 설치(`dependency_versions_not_pinned`). tsc·type-aware ESLint는 받은 뒤에도 `dependency_unavailable`이다. 테스트가 빌드 산출물(`'..'`)을 import하고 `@types/mocha`를 선언하지 않아서이며, 손으로 실행한 tsc에서 받기 전 TS2307 다섯이 받은 뒤 공백 둘(TS2307 `'..'`, TS2593)로 줄었다. 관찰 20·발견 20은 기준선 ESLint다.
  - 로컬 폴더(zustand 사본): 받은 뒤 tsc·type-aware ESLint가 위와 같은 결과를 냈고 원본 트리의 파일 목록과 hash가 그대로였다. 모든 run 뒤 에이전트 디렉터리에 `node_modules`가 남지 않았다(작업 사본과 함께 지워짐). 위 성공 결과는 최종 커밋으로 다시 실행해 같았고, 빌드 스크립트 끔의 hexyl에는 `dependency_versions_not_pinned` 공백이 없다.
  - 닿지 않는 레지스트리(`npm_registry` `http://127.0.0.1:9/`): `deps.javascript` `failed`/`registry_unreachable`, 분석은 계속됐다(run 118초). 처음에는 npm 기본 재시도가 5분 넘게 이어져 모듈 제한시간(`timed_out`)으로 끝났고, 재시도를 줄여 고쳤다. 받는 중 취소(빈 pnpm 저장소에서 환경 단계 진행 중): run `cancelled`, 환경 단계 task 둘 `cancelled`, 에이전트 기록의 attempt 정리 `confirmed`, 남은 pnpm 프로세스와 `node_modules` 없음. 1000바이트 크기 상한: `budget_exhausted`/`download_limit_exceeded`. 실모델 호출은 하지 않았다.
- 확인 환경(Python): Linux(WSL2), Go 1.27.4, PostgreSQL 17.10, Python 3.14.4, uv 0.12.5, mypy 2.4.1, pyright 1.1.414(Node 24.18.0), ruff 0.16.3, bandit 1.9.4. 임시 설정·상태 디렉터리, 모델 없음(`OPENAI_API_KEY` 없음)으로 `codeye up`을 실행했다. `encode/httpx`(커밋 `7cff5f7`)는 받기 끔에서 mypy `import_unresolved` 63·`stub_missing` 3, pyright `import_unresolved` 68·`import_source_missing` 2, 관찰 168·발견 129였고, 받기 켬에서 `deps.python`이 27개 중 26개를 받고 wheel이 없는 `mkautodoc`을 뺐으며(`partial`/`no_binary_distribution`, 고정 안 됨이라 attempt 트리) mypy `import_unresolved` 2·`stub_missing` 4, pyright `import_unresolved` 1, 관찰 184·발견 145(pyright `reportArgumentType` 17개가 새로 나옴)였다. 고정된 `requirements.txt`(attrs·idna)의 작은 로컬 프로젝트는 받기 끔에서 mypy·pyright가 attrs 클래스 생성자를 모르고 `call-arg`·`reportCallIssue` 2개씩을 냈고(받기 공백 `dependencies_not_fetched` 둘), 받기 켬에서 그것이 사라지고 실제 결함인 `arg-type`·`return-value`(pyright `reportArgumentType`·`reportReturnType`) 3개씩이 나왔다. 두 번째 run은 설치 없이 공유 트리를 썼다(`prepared_tree_reused`). 고정하지 않은 같은 프로젝트는 attempt 트리와 `dependency_versions_not_pinned`였다. 닿지 않는 index(`http://127.0.0.1:9/simple`)는 `deps.python` `failed`/`registry_unreachable`로 끝나고 분석은 계속됐다. `uv.lock`이 있는 `pallets/click`(로컬 사본)은 루트 모듈이 `uv export`로 73개를 고정해 모두 받고(공유 트리, 받은 패키지의 `.pth`를 지워 `interpreter_startup_hooks_removed`) 문서 예제 모듈 10개는 고정 안 된 `requirements.txt`로 받았다. mypy `import_unresolved`는 1, pyright는 0이었다. 네 로컬 프로젝트의 파일 hash와 항목 수는 실행 전후 같았다. httpx 수치는 임시 디렉터리 크기 감시와 dependency group 상한을 더하기 전 빌드의 것이고, 나머지는 마지막 빌드의 것이다. 모든 보고서가 `partial`인 이유는 모델 검토가 없어서다.
- 두 작업을 합친 빌드로 다시 확인(새 설정·상태 디렉터리, 모델 없음, `tools.json` 23개 도구 모두 `found`): `spf13/cobra`(`adbc881`) `deps.go` 6/6, 관찰 22·발견 2. `sharkdp/hexyl`(`6ecc29b`) 빌드 스크립트 끔은 `deps.rust` `dependency_consumers_not_allowed`, 켬은 67/67과 cargo check `succeeded`·clippy 관찰 2, 관찰 16·발견 2. `pmndrs/zustand`(`9fed2eb`) pnpm 11.28.13로 558/558, tsc 진단 0·type-aware ESLint 관찰 268, 관찰 455·발견 409. `encode/httpx`(`7cff5f7`) uv 0.12.5로 27개 중 26개(`partial`/`no_binary_distribution`), mypy `import_unresolved` 2·`stub_missing` 4, pyright `import_unresolved` 1, 관찰 184·발견 145. 위에 적은 각 작업의 수치와 같다.

### 추가 — 검토 우선순위와 읽을 수 있는 보고서

- **검토 우선순위**: 계획은 slot 수만 정하고 정적 분석 뒤의 `review.scope`가 파일을 채운다. 주요 파일이 부차 경로(`fixtures`·`testdata`·`examples`·`vendor`·생성 파일 등) 파일보다 먼저이고, 같은 분류 안에서 도구 진단(심각도·수), 필수 분석이 실패한 모듈, 진입점, 그 밖의 소스, 테스트 순이다. 큰 모듈이 먼저 더 받되 다른 주요 모듈도 받고, slot은 되도록 한 모듈·한 디렉터리다. overview 조사도 같은 분류를 따른다. slot 상한은 32에서 96, 호출 상한 최댓값은 207이다. 규칙은 한 곳(`record.DefaultSecondary`)이고 worker 설정으로 바꿀 수 있다([검토 우선순위](docs/REVIEW.md#검토-우선순위), A45).
- **범위 산출물 0.2와 보고서 builder 8**: 범위 산출물이 파일마다 분류·순위·이유와 쓴 규칙을 싣는다(0.1도 읽는다). 장부에 모듈 분류, 부차 파일 수·보낸 수·예산 밖 수, 거절된 주요 slot 수가 더해지고, 종합에 주요·부차 수, 먼저 볼 항목 `review_primary_not_sent`, 공백 `review_scope_secondary_not_reached`가 생긴다.
- **요약 보기**: Markdown·HTML의 기본이 요약 보기다. 절을 가치 순서(코드 분석, 정적 분석, 지표, 비밀정보, 환경, 문서, 공백)로 두고, 종합에 의미 있는 수를 문장으로 쓰며, 정적 분석을 언어·분석기·규칙으로 묶어 위치 5곳과 "외 N곳"을 보이고, 부차 경로 항목은 주요 경로 뒤에 짧게 둔다. `detail=full`은 이전 배치와 바이트가 같다. 렌더링만 바뀌므로 저장 본문과 JSON은 그대로이고 이전 리비전도 요약으로 보인다([내보내기 상세도](docs/REPORT.md#내보내기-상세도), [API](docs/API.md)). 웹 보고서 패널도 같은 묶음이고 전체 목록은 펼칠 때만 그린다. "더 나은 결과를 얻으려면"은 거절된 slot을 모두 검토할 호출 상한을 센다.
- **고침**: 먼저 볼 항목 문장에 인자가 없는 문구가 `%!(EXTRA int=1)`로 보이던 문제. 장부 표에 부차 경로·예산 밖 열이 없어 행의 합이 대상 수와 맞지 않던 문제.
- **수치**: 자체 분석 보고서(builder 6)는 Markdown 19,096줄·HTML 3.27 MB에서 요약 보기 782줄·98 KB다. 아래 파일럿(상한 99)의 builder 8 보고서는 전체 18,864줄, 요약 968줄이다. 이 저장소(커밋 `ee577af`)의 파일럿을 가짜 upstream으로 다시 실행한 결과(변경 전 `ee577af` builder 7 → 후 이 변경의 마지막 코드 커밋 `41dbc0e` builder 8. 중간 커밋 `039dc57`의 결과와 같다). 상한 45: 호출 41회 동일, slot 32 → 96, fixtures 모듈 42개에 보낸 파일 80 → 0, `go:.` 525개 중 보낸 파일 11 → 65, 모델 발견 19건 중 fixtures 16 → 1. 상한 99: 호출 69 → 95회, `go:.` 보낸 파일 26 → 178, fixtures 보낸 파일 100 → 0, 모델 발견 33 → 46건(fixtures 21 → 1). 두 상한 모두 주요 파일 179개(`go:.` 155, `web` 24)는 slot 96개 밖이라 계획되지 않았다. 상한 99의 요청 바이트는 1.73 MB → 2.64 MB로, slot 32개가 먼저 다하던 기본 설정의 비용이 큰 저장소에서 늘 수 있다. 가짜 upstream의 발견 수는 경로 검증이며 검토 품질의 근거가 아니다. 회귀 집합 dry run의 요청 57건은 변경 전과 바이트 단위로 같다.
- 확인 환경: Linux(WSL2), Go 1.27.4, PostgreSQL 17.10, Node 24.18.0, 가짜 모델 upstream(실모델 호출 없음).

### 추가 — Go 의존성 받기

- **의존성 준비**: 분석 전에 Go 모듈마다 `deps.go`·`deps.go.mNN` task가 의존성을 받고(`go mod download -json -modfile=<사본>`), go vet·Staticcheck는 그 캐시를 오프라인으로 읽는다. 받기는 실행 사본의 모듈 디렉터리에서 하고 원본·snapshot·실행 사본의 go.mod·go.sum을 바꾸지 않는다. 필수 task가 아니어서 실패해도 분석은 계속하고 사유가 남는다([의존성 준비](docs/ARCHITECTURE.md#의존성-준비), A42).
- **선택과 설정**: 프로젝트 선택 `fetch_dependencies`(기본 켬, 화면 "고급"의 "의존성 받기")와 노드 스위치 `--dependency-fetch`(개인 모드는 켬). `--dependency-cache-dir`, `--dependency-timeout`(10분), `--dependency-total-timeout`(20분), `--dependency-max-bytes`(2 GiB), `--go-proxy`·`--go-sumdb`·`--go-private`·`--go-noproxy`·`--go-nosumdb`, 개인 모드 `agent.dependencies`. 개인 모드는 사용자 `go env`의 받기 설정을 따르고(`tools.json`의 `go_settings`), 비공개 모듈은 공개 proxy에 묻지 않는다. 받은 캐시는 `state_dir/cache/deps`이고 cache 저장 영역에 센다.
- **기록과 보고서**: task마다 의존성 기록(`application/vnd.codeye.dependencies+json` 0.1, [schema](schemas/dependencies.schema.json), [예시](examples/dependencies.json))을 올린다. 보고서 builder 7은 환경 절에 `dependencies` 표와 공백 `dependencies_not_prepared`·`dependencies_not_fetched`·`dependency_versions_not_pinned`를 더한다. 웹은 그 표와 "더 나은 결과를 얻으려면"의 "의존성 받기 켜고 다시 시도"를 보인다.
- **고침**: 모델이 설정되지 않은 개인 모드 run에서 모듈별 검토 장부가 있으면 보고서 게시가 거부되던 문제(장부가 보내지 않은 호출까지 세어 "ledger counts fewer calls", 요청 바이트 불일치). 장부는 요청이 있는 호출만 센다.
- 확인 환경: Linux(WSL2), Go 1.27.4, PostgreSQL 17.10, Node 24.18.0, Playwright Chromium, staticcheck 2026.2.2. 임시 설정·상태 디렉터리, 빈 Go 모듈 캐시, 모델 없음으로 `codeye up`을 실행했다. `spf13/cobra`(커밋 `adbc881`)는 받기 끔에서 보고서 `partial`/`inconclusive`, 관찰 20·발견 0, go vet·Staticcheck `failed`/`dependency_unavailable`(공백 `dependencies_not_fetched` 둘)였고, 받기 켬에서 `deps.go` 6/6 받음, go vet·Staticcheck `succeeded`(`dependencies_prepared`), 관찰 22·발견 2(Staticcheck SA1029)였다. 두 보고서 모두 `partial`인 이유는 모델 검토가 없어서다. 의존성 있는 로컬 폴더는 받은 뒤 go vet이 printf 결함을 찾았고 원본 트리의 파일 hash가 그대로였다. 닿지 않는 proxy는 `registry_unreachable`로 끝나고 분석은 계속됐다. 받는 중 취소는 `deps.go` `cancelled`, 정리 확인(`confirmed`), 남은 `go mod download` 프로세스 없음이었다. 1000바이트 크기 상한은 `budget_exhausted`/`download_limit_exceeded`였다(첫 측정 전에 약 52 MB를 받았다. 2초 간격의 soft 제한이다). 화면에서 받기를 끈 프로젝트의 "의존성 받기 켜고 다시 시도"가 선택을 켜고 다시 실행해 받는 것을 확인했다. 실모델 호출은 하지 않았다.

### 고침 — 실제 저장소에서 한 곳의 문제가 작업 전체를 실패시키던 것

- **작업 사이 되돌리기**: 한 attempt의 작업은 작업 사본 하나를 차례로 쓴다. Rust build script(`fixtures/rust/drift`)가 고쳐 쓴 `src/lib.rs`가 뒤의 lizard·jscpd·gitleaks를 `workspace_drift`로 실패시켰다. 이제 워커가 작업마다 사본을 스냅샷 내용으로 되돌리고(바뀐 manifest 파일은 다시 쓰고 manifest에 없는 항목은 지운다. 숨김 이름·`node_modules`·`vendor`·`testdata`는 남긴다) `workspace_restored_after_task`·`workspace_leftovers_removed`(실패하면 `workspace_restore_failed`)를 남긴다. 자기 실행 중에 사본을 바꾼 작업은 여전히 `workspace_drift`다.
- **쓸 수 없는 비밀값 선별**: 비밀값 작업이 실패하거나 부분 결과여도 검토를 건너뛰지 않는다(`secret_screen_failed` 없앰). 미선별처럼 기본 읽기 필터만 거쳐 보내고 `model_input_not_secret_screened`를 남기되, 읽을 수 있는 결과가 보고한 파일은 계속 뺀다. 보고서(Markdown·웹)는 쓸 수 없던 작업과 상태·사유를 적는다. 이전 worker의 `secret_screen_failed` 기록은 그대로 읽는다. 보고서 builder 버전은 그대로다.
- **해석하지 못한 파일**: ruff·mypy·pyright·bandit과 ESLint는 구문 분석하지 못한 파일만 빼고(`syntax_error`·`parse_error` 단위, `unparsable_files_not_analyzed`) 나머지 결과로 성공한다. 위치가 있는 구문 오류는 그 파일의 관찰(ESLint `parse-error`)이다. 파일 이름은 대상 속성 `unparsable_files`에 남는다. mypy는 구문 오류 파일을 빼고, 남은 파일이 import하면 그 모듈을 건너뛰게 하여(`follow_imports = skip`) 다시 실행하며(`mypy.check.r01`…) 재실행은 작업 제한시간의 남은 시간만 쓴다(다 쓰면 `timed_out`, `mypy_reruns_time_limit_reached`). 해석할 수 있는 파일이 없을 때만 실패다. tsc·C/C++·go vet·Staticcheck·cargo의 판정은 바꾸지 않았다.
- 확인 환경: Linux(WSL2), Go 1.27.4, PostgreSQL 17.10, 가짜 모델 upstream. 이 저장소를 `codeye up`으로 분석한 작업 상태가 고치기 전 `succeeded` 79·`skipped_policy`(`secret_screen_failed`) 70·`workspace_drift` 5·`source_syntax_errors` 4에서 고친 뒤 `succeeded` 156·`workspace_drift` 2(`fixtures/rust/drift`의 cargo check·clippy, 의도한 결과)가 됐다. 실모델 호출은 하지 않았다.

### 추가 — 경로·Git 주소 하나로 분석, 프로젝트별 분석 선택, 다시 시도

- **Git 주소**: `codeye up https://github.com/OWNER/REPO`(`…/tree/BRANCH`, `https://` 생략 가능)가 프로젝트를 등록하고 분석을 요청한다. 브랜치를 비우면 확보할 때마다 원격의 기본 브랜치를 쓰고 manifest에 남긴다. `git_hosts`(기본 `["github.com"]`)마다 provider를 등록하고 찾은 git과 함께 에이전트에 넘긴다. 인증을 요구하는 저장소는 `source_auth_required`, 닿지 않는 호스트는 `source_unreachable`로 실패한다. 비공개 저장소는 지원하지 않는다.
- **첫 화면**: 개인 모드에서 "폴더 경로 또는 Git 주소" 한 칸과 분석 버튼으로 등록·요청하고 바로 실행 화면으로 간다(같은 폴더·저장소는 같은 프로젝트, 진행 중인 실행은 그대로 연다). 고급(접힘)에 이름·브랜치·두 분석 선택이 있다. 게시된 보고서는 저절로 열린다. 실패·일부 완료·취소된 실행에 "다시 시도"가 있다. 보고서 위의 "더 나은 결과를 얻으려면"이 공백에서 없는 도구의 설치 방법, 모델 키, 호출 상한, 빌드 스크립트(켜고 다시 시도)를 안내한다. "도구" 메뉴가 찾은 도구·모델·Git·읽을 수 있는 폴더를 보인다(서버 모드는 진단 파일 보기 그대로).
- **API**: `POST /projects`의 `local_path`(개인 모드, 절대 경로를 허가된 root에 대응)와 `analysis`, 같은 소스의 200 응답, `PUT /projects/{id}/analysis`, `POST /runs/{id}/retry`(`same_run`·`new_run`), `GET /setup`. Git `branch`는 선택이다. 노드 job에 `analysis`가 실리고 에이전트에 `--project-analysis-policy`·`--protected-dir`가 생겼다. migration은 없다(설정 JSON).
- **프로젝트별 분석 선택**: "소스를 모델에 보냄"과 "저장소의 빌드 스크립트 실행"을 프로젝트마다 정하고 개인 모드 노드가 로컬·Git 모두 따른다. 기본값이 바뀌었다: 로컬 폴더는 둘 다 켬(이전에는 빌드 스크립트가 root별 허가로 기본 끔), Git 저장소는 모델 전송 켬(이전에는 보낼 방법이 없었다)·빌드 스크립트 끔. `source_roots[].model_review`·`repository_code`는 개인 모드에서 쓰이지 않는다. 서버 모드 노드는 운영자 허가 그대로다.
- **홈 디렉터리 root와 데이터 폴더 경계**: 개인 모드는 홈을 설정에 쓰지 않는 root `home`으로 둔다. 상태·백업 디렉터리를 품거나 그 안에 있는 프로젝트 폴더는 어느 형식으로 등록하든 `FOLDER_HOLDS_CODEYE_DATA`(문제의 폴더를 적는다)로 거절하고 에이전트도 확보 전에 거절한다(`source_holds_codeye_data`). 소스 확보의 data root와 실행기 보호 경로는 root 전체가 아닌 프로젝트 폴더 기준이 됐다.
- **기본값**: 개인 모드 `agent.max_workers`는 min(4, 논리 CPU/2)이다. 모델이 없으면 모델 검토 사유가 `model_not_configured`다(이전에는 `model_source_not_approved`로 보일 수 있었다).
- 확인 환경: Linux(WSL2), Go 1.27.4, PostgreSQL 17.10, Node 24.18.0, Playwright Chromium, git 2.53.0, cargo 1.99.1. 임시 HOME에서 실제 공개 GitHub 저장소(BurntSushi/termcolor)를 `codeye up 주소`와 화면으로 분석했고(기본 브랜치 `master`의 커밋 고정, cargo check·clippy가 빌드 스크립트 끔으로 빠짐, 켜고 다시 시도하면 실행되어 오프라인 의존성 때문에 `dependency_unavailable`), 없는 저장소의 실패와 다시 시도를 화면에서 확인했다. CI는 시험 빌드의 file:// 미러를 쓴다. 실모델 호출은 하지 않았다.

### 추가 — `codeye up`과 관리형 PostgreSQL

- **`codeye up [PATH]`**: 설정 파일이 없으면 `schema_version`만 담아 만들고, PostgreSQL을 상태 디렉터리에 초기화해 띄우고, schema를 만들거나 맞추고, controller와 에이전트를 시작한다. PATH를 주면 그 폴더를 source root로 설정에 더하고(이미 있는 root 안이면 그 root와 상대 경로) 프로젝트를 등록하고 분석을 요청한다. 일회용 로그인 링크(프로젝트와 실행으로 가는 `#project=…&run=…` 포함)를 출력하고 브라우저를 연다(WSL은 `wslview`·`explorer.exe`). 이미 실행 중이면 바뀐 것만 반영한다. 에이전트 설정이 바뀌면 drain 뒤 다시 띄운다. Git URL은 아직 받지 않고 clone 안내를 낸다. `codeye open`은 새 링크만 발급하고 연다.
- **관리형 PostgreSQL(기본 `database.mode`)**: 설치된 PostgreSQL 14 이상(`--postgres-bin-dir`·`database.postgres_bin_dir`·`CODEYE_POSTGRES_BIN_DIR`·PATH·`/usr/lib/postgresql/*/bin` 등)으로 `<state_dir>/postgres/data`에 cluster를 만들고 127.0.0.1의 빈 포트(55432부터), scram-sha-256, 생성 비밀번호(`postgres/password`, 0600)로 띄운다. 데이터가 있는 이전 schema는 `pg_dump` 사본을 먼저 쓰고 자동 migration한다. 셸의 `PG*` 변수와 `CODEYE_DATABASE_URL`은 무시하고 알린다. `stop`이 PostgreSQL도 멈추고(`database` 필드), `status`가 상태·포트·버전을 보이고, `backup`·`restore`가 그 cluster를 쓴다(멈춰 있으면 그 동안만 띄운다). 프로그램이 없거나 데이터 디렉터리와 주 버전이 다르면 할 일을 알리는 오류다. external 모드는 그대로다.
- 설정 기본값: `state_dir` `~/.local/state/codeye`, `source_roots` 비어도 됨, `web_dir` 실행 파일 옆 `web/dist`, 백업 위치 `<state_dir>-backups`. `listen`을 적지 않았으면 7331이 쓰일 때 7340까지 다음 빈 포트를 쓴다.
- 개인 모드 controller(`codeye serve --personal`)의 브라우저 세션은 7일이다.
- 확인 환경: Linux(WSL2), Go 1.27.4, PostgreSQL 17.10(관리형 cluster와 smoke의 임시 서버), Node 24.18.0, Playwright Chromium. 임시 설정·상태 디렉터리로 `up` → Go 분석 보고서 → 출력 링크로 Chromium 로그인 → `stop`(PostgreSQL 정지) → `backup` → `up` 재사용 → 새 상태 디렉터리로 `restore` → `up`을 실행했다. 브라우저 자동 열기 프로그램은 실행하지 않았다.

### 추가 — 도구 자동 탐지

- **도구 탐지**(`internal/toolfind`): go, git, node, OpenCode와 분석기 도구 열네 개를 사용자의 PATH, `~/.local/bin`, `~/go/bin`, `/usr/local/go/bin`, `~/.opencode/bin`, CODEYE 도구 접두사 `~/.local/share/codeye-tools/{bin,cpp/bin,go/bin}`에서 찾는다. JS 도구(pyright, eslint, tsc, jscpd)는 `node_modules`의 진입 스크립트(도구 접두사와 찾은 node 옆, PATH의 npm shim은 가리키는 스크립트로 푼다), cargo·clippy는 `~/.rustup/settings.toml`의 기본 toolchain(없으면 나머지 toolchain)의 실제 바이너리를 쓰고 rustup proxy는 쓰지 않는다. 후보마다 에이전트가 시작할 때 하는 검증과 같은 것(`builtin.CheckTool`: 분석기 생성의 모양 규칙과 버전 probe)을 하므로 찾은 도구는 에이전트가 받아들이는 도구다. 상태는 `found`·`not_found`·`unusable`이고 경로, 버전, 시험한 계열 여부, 찾은 위치, 이유, 한 줄 추가 방법을 기록한다. 시험한 계열 밖의 버전은 `tool_version_untested`로 기록하고 쓴다. 찾은 go의 `go env GOMODCACHE`를 모듈 캐시로 넘기고(go vet은 `GOPROXY=off`), mypy와 pyright가 모두 있으면 `--python-both-type-checkers`를 함께 준다. 명시 설정은 항상 우선한다. 결과는 `<state_dir>/tools.json`([schema](schemas/tools.schema.json), [예시](examples/tools.json))이다.
- **`codeye tools [--config PATH] [--json]`**: 찾은 것·없는 것·추가 방법의 표를 보인다. 설정 파일이 없어도 동작하고 아무것도 설치하지 않는다.
- **`agent.auto_detect`**: 개인 모드 설정. `false`이면 탐색하지 않고 명시 설정만 쓴다.
- **`--advertise-without-baseline`**(에이전트 플래그, worker hello `advertise_without_baseline`, `builtin.Config.AdvertiseWithoutBaseline`): 기준 분석기가 하나도 없어도 분석 capability를 광고한다. 쓸 수 없는 도구의 작업은 `unsupported`로 기록되고 run은 부분 보고서로 끝난다. 기본은 꺼져 있다.
- 확인 환경: Linux, 이 머신의 `~/.local/share/codeye-tools`·`~/.local/bin`·`~/.rustup`·`~/.opencode`. 가짜 도구 트리 단위 시험과 실제 머신 시험(`CODEYE_TEST_REAL_TOOLS=all`), 탐색 결과를 넘긴 개인 모드의 Go(모듈 캐시의 의존성 포함)·Python·JS 분석에서 `tool_not_configured`가 없음을 확인했다.

### 변경 — `codeye doctor`

- 도구를 PATH 조회 대신 도구 탐지로 찾는다. 에이전트가 쓸 도구와 같은 결과이고, JSON 보고서의 `tools` 배열(경로 없음)과 텍스트의 도구 표로 보인다.
- 항상 `unverified`이던 `model`·`process_control` 항목과 사유 값 `adapter_contract_pending_w10`·`native_executor_pending_w05`·`runtime_process_control_not_probed`를 없앴다. `python`·`c_cpp`(PATH의 `python3`·`cc`) 항목도 에이전트가 쓰지 않으므로 없앴다. `analyzers`(기준 분석기가 하나라도 쓸 수 있는가)와 `configured_tools`(설정에 적은 도구가 쓸 수 있는가)가 생겼다.
- 종료 코드는 이 모드에서 중요한 항목(`checks[].blocking`)이 실패했을 때만 1이다. 이전에는 늘 1이었다.

### 추가 — API 키로 모델 설정

- **일반 모델 공급자**: 모델 ID는 `PROVIDER/MODEL`이면 받는다(이전에는 `opencode/*`와 QA relay만). 워커가 API 키 파일을 모델 호출마다 읽어 `OPENCODE_CONFIG_CONTENT`의 provider 설정으로만 넘기고 기록에서 가린다. 에이전트 `--model-credential-file`, `--model-timeout`(기본 180초, 이전 75초 고정), Hello `model_credential_file`·`model_timeout_seconds`. 키 파일은 CODEYE 형식(`provider`, `api_key`, 선택 `base_url`·`openai_compatible`) 또는 OpenCode `auth.json`의 `api` 항목이다. QA relay는 이 공급자의 한 사례이고 프로필 바이트는 그대로다.
- **개인 모드**: `agent.model` 없이도 `<state_dir>/secrets/model.json`이나 OpenCode `auth.json`의 키로 모델을 쓴다. 기본 모델은 OpenAI `gpt-6.2-sol`, Anthropic `claude-opus-5-5`, OpenCode Zen `gpt-6.1-sol`이고 `agent.model.name`으로 바꾼다. `agent.model`의 `opencode`·`name`은 선택이 됐고 `credential_file`·`timeout_seconds`가 생겼다. 모델이 설정되면 로컬 source root의 모델 검토를 기본으로 허가한다(`model_review: false`로 뺀다). `personal.WriteModelSecretFromEnv`가 `OPENAI_API_KEY`·`ANTHROPIC_API_KEY`·`OPENCODE_API_KEY`에서 키 파일을 쓴다(`codeye up`이 부른다).
- **OpenCode 버전**: 시험한 1.19.5가 아닌 릴리스도 실행하고 기록·보고서 한계에 `opencode_version_untested`를 남긴다.
- **`codeye model probe`**: 인자 없이 개인 설정의 모델을 확인하고 provider·모델·OpenCode 버전, 성공 또는 실패한 단계와 사유를 사람이 읽는 문장으로 출력한다(`--json`, `--credential-file`). 실패 사유는 `model_auth_failed`·`model_not_found`·`model_rate_limited`·`model_credential_unavailable`로 나뉘고 보고서가 한국어로 설명한다. 확인이 실패해도 분석은 실행된다.
- `scripts/fake_model_upstream.py`가 OpenCode의 OpenAI provider가 직접 보내는 스트리밍 Responses 요청과 `probe` 역할에 답하고, `--mode unauthorized`(401)를 갖는다. `scripts/test_postgres.py --personal-model`이 실제 OpenCode와 가짜 upstream으로 개인 모드 모델 경로 전체를 확인한다.
- 확인 환경: Linux(WSL2), Go 1.27.4, PostgreSQL 17.10(임시 cluster), OpenCode 1.19.5, 가짜 upstream. 실모델 호출은 하지 않았다. OpenAI가 `gpt-6.2-sol` 요청을 받는지는 확인하지 않았다.

### 추가

- **분석 종류별 절**: 보고서가 종합, 환경 분석, 문서 분석, 정적 분석(언어·도구별), 구조·중복 지표, 비밀정보(값 없음), 코드 분석(모델 검토), 분석 공백 순의 절로 나뉜다. 선택 최상위 블록으로 더했고 기존 필드는 바이트 단위로 같다(report 패키지 시험 입력 84개를 이전 builder 출력과 대조). 보고서 builder는 5이며, 모든 관찰과 발견이 한 절에 한 번씩 들어가는지를 저장·조회 검증이 확인한다. 상태·품질 판정 규칙은 그대로다. 모델 문장은 수락된 역할 기록에서만 오고 "모델 해석"으로 표시하며 코드 미포함 내보내기에서는 생략한다. Markdown·HTML·웹 화면은 같은 수를 보이고, 절이 없는 이전 리비전은 이전 평면 배치로 보인다. "이전 실행과 비교"는 웹 화면에만 있다. 분석기 envelope와 descriptor에 분석기 목적(`analyzer.purpose`: 지표·비밀정보)을 기록해 어느 절로 갈지 정한다. [`examples/report.json`](examples/report.json)과 [`examples/report.md`](examples/report.md)는 실제 worker 파이프라인과 대본 모델 응답으로 다시 만들었다(합성 예시, 실모델 호출 없음).

### 추가 — 모듈 단위 검토 분할

- overview가 보내지 못한 코드·테스트 파일을 모듈 단위 slice(`review.investigate.sNNN`, `review.verify.sNNN`, 최대 32개)로 검토한다. 계획은 manifest·scope·상수로만 정하고, `review.scope`가 overview가 실제로 보낸 파일을 빼고 범위 산출물(`application/vnd.codeye.review-scope+json` 0.1)을 쓴다. slice 기록은 schema 0.3이다. 지침 문구는 바뀌지 않았고 slice 요청의 지침 hash는 overview와 같다.
- attempt당 모델 호출 상한 `codeye agent --review-max-calls`(기본 99, 15~207), Hello `review_max_calls`, 개인 설정 `agent.model.review_max_calls`. 전체 역할 다섯 개가 먼저 실행되고(최대 15회), slice는 남은 호출이 6 이상일 때 차례로 승인된다. 거절된 slice는 `budget_exhausted`(`review_budget_exhausted`)다. 기본 99는 중간 규모 저장소의 대부분을 기본 설정으로 검토하기 위한 값이다. 유료 회귀 실행에서 상한 33은 파일럿 Go 모듈 67개 파일 중 34개를 28회 호출로 검토했다. 비용과 낮추는 방법은 [안내서](docs/GUIDE.md)에 있다.
- slice는 전체 역할과 같은 비밀값 선별 규칙을 따르고 선별이 없다고 막지 않는다. 선별이 끝났으면 보고된 파일을 빼고, gitleaks가 없거나 실행되지 않았으면 기본 읽기 필터(민감값 패턴·보류 파일)만 거친 입력으로 실행한다. 이때 범위 산출물은 `screen: not_screened`, 범위 산출물과 slice 기록은 `model_input_not_secret_screened`를 싣고, 보고서는 비밀정보 절 `model_input`의 미선별 상태와 공백 `model_input_not_screened`를 보인다. 선별 결과를 쓸 수 없으면 전체 역할처럼 `secret_screen_failed`로 건너뛴다. gitleaks는 지원 언어 소스가 없어도 안내 문서·모듈 manifest 같은 모델 입력 파일이 범위에 있으면 계획된다.
- `scripts/regression_pilot.py`가 gitleaks 경로와 호출 상한을 에이전트에 넘기고 slice 조건을 보고서 장부로 검사하며, `--dry-run`(시험 장부의 relay만, 가짜 upstream과 이 호스트의 미설정 도구 때문인 부분 완료는 따로 적는다)과 `--compare-dumps`를 갖는다. `scripts/quality_eval.py`의 상한 추정은 slice가 있는 변형을 호출 상한까지로 본다.
- 보고서 builder 6: slice 기록과 범위 산출물을 읽고, slice 후보를 `review_sNNN_<후보>` 발견으로 코드 분석 절에 넣는다. 코드 분석 절의 [모듈별 검토 장부](docs/REPORT.md#모듈별-검토-장부)(`code_review.ledger`)가 상한·호출·요청 크기와 모듈마다 모델에 보낸 파일·보내지 못한 이유를 센다(모델에 보낸 범위는 검증 범위가 아니다). `llm_exposed_files`는 slice도 세고, `review_calls[]`는 `task_key`를 싣고 최대 207개다. 종합에 보낸/보내지 않은 파일 수, 분석 공백에 `review_scope_*`가 생기며 Markdown·HTML·웹 화면이 같은 수를 보인다. 비밀정보 절 `model_input`에 `screened`가 생겼다. 예시 [`examples/report-slices.json`](examples/report-slices.json)·[`.md`](examples/report-slices.md)는 coverage fixture를 상한 15로 실행한 합성 예시다.
- 확인 환경: Linux, Go 1.27.4, PostgreSQL 17.10(임시 cluster), gitleaks 8.30.1, OpenCode 1.19.5와 가짜 upstream(`--test-upstream`). 실모델 호출은 하지 않았다.

### 추가 — 계정·권한·감사

- **로컬 계정**: 사용자 이름(대소문자 무시)과 비밀번호로 로그인한다(`POST /session`). 비밀번호는 PBKDF2-HMAC-SHA256(600,000회, 사용자별 salt)으로 저장하고, 없는 사용자와 틀린 비밀번호는 같은 응답·같은 비용이다. 로그인 링크와 개인 모드는 그대로 동작한다.
- **관리자 재설정의 기록과 표시**: 로그인 링크마다 발급자와 목적을 저장하고, 관리자 링크로 시작한 세션의 로그인과 모든 감사 행에 `link_issued_by`를 남긴다. 계정 생성·재설정 링크의 세션은 세션 행에서 비밀번호 변경에 묶인다. 사용자 이름 없는 사용자의 관리자 재설정은 409다. 세션 조회의 `password_reset`이 재설정한 관리자와 시각을 30일 동안 지울 수 없게 보여 준다. 관리자가 재설정으로 계정을 넘겨받는 것은 막지 않고 기록·표시한다.
- **로그인 제한**: (이름, 접속 주소) 쌍 5회 실패면 그 쌍만 30초부터 두 배씩 최대 5분 지연한다. 주소만으로는 500회에서 5분 지연한다. 이름 전체를 잠그는 규칙은 없다. 카운터는 DB에 있어 여러 controller가 공유하고, 15분 지난 행은 지운다. 지연된 요청은 429와 `Retry-After`다. 비밀번호 KDF 동시 계산은 프로세스마다 CPU 수로 제한하고 3초 안에 자리가 없으면 503이다.
- **비밀번호 변경·재설정**: `POST /session/password`는 다른 세션을 끝낸다. 관리자 재설정은 비밀번호를 지우고 일회용 로그인 링크를 주며, 사용자는 새 비밀번호를 정하기 전에는 다른 경로를 쓸 수 없다.
- **관리자**: 사용자 목록·생성·비활성화, 관리자 지정·해제, 재설정, source root 허가, provider·감사 기록 조회(`/api/v1/admin/…`, `/api/v1/audit`). 관리자 역할은 프로젝트를 열지 않는다. 마지막 활성 관리자는 남는다. 첫 관리자와 계정 운영은 `codeye user create --username … --admin`, `user passwd`, `user list`, `user disable`, `user admin --set/--unset`. 개인 모드 사용자는 관리자다.
- **프로젝트 구성원**: `GET /projects/{id}/members`, `POST /projects/{id}/members`(정확한 사용자 이름), `PUT/DELETE /projects/{id}/members/{user_id}`. 프로젝트 응답에 호출자의 `role`, `can_run`, `can_manage`가 붙는다.
- **계정·구성원 화면**: 사용자 이름·비밀번호 로그인, 비밀번호 변경, 계정 메뉴, 프로젝트 구성원 관리(아이디로 추가), 역할에 따른 버튼 제한, 관리자 영역(사용자·source root 허가·provider·감사 기록), 프로젝트 감사 기록 탭.
- **감사 기록**: 추가 전용 `audit_events`(migration 0010). 등록·변경·실행·취소·내보내기·판정·예약·구성원·계정·로그인·노드·배치 한도·오래된 epoch 거부를 변경과 같은 트랜잭션에 남긴다. 기록하지 못하면 변경도 하지 않는다. 비밀번호·토큰·경로·모델 입력은 담지 않는다.
- **TLS 수신**: `codeye serve --tls-cert --tls-key --public-origin`으로 loopback이 아닌 주소에서 HTTPS로 받는다(`__Host-` Secure cookie, HSTS). TLS 없이 loopback이 아닌 주소로는 시작하지 않는다. reverse proxy 뒤 배포는 지원하지 않는다.
- **격리 시험**: 라우터의 모든 경로를 여섯 행위자로 요청해 404·403과 감사 행을 확인하는 표 시험(TC-140~TC-157). 웹 화면은 아직 없다.
- 확인한 환경: Linux(WSL2), PostgreSQL 17.10, Go 1.27.4.

### 변경

- **문서 통합 (#37)**: 번호를 붙인 단계별 문서 50개를 주제별 문서 16개(`docs/`)와 이 변경 이력으로 바꿨다. 주제 문서에는 현재 유효한 계약과 운영 규칙만 두고, 코드에서 문서를 가리키는 참조는 절 anchor까지 검증한다.
- **단계 이름 제거 (#38)**: 코드 주석, 파일·시험·식별자 이름, CLI 문구, 스크립트·fixture 디렉터리 이름, 시험용 환경변수에서 구현 단계 번호를 뺐다. 예산 relay는 `scripts/model_budget_relay.py`, 평가 runner는 `scripts/quality_eval.py`, 회귀 집합은 `fixtures/regression/`이다. `scripts/validate_bundle.py`가 단계 표기가 다시 들어오는 것을 막는다.
- OpenAPI의 구현 표시는 `x-codeye-status: implemented` 하나다.
- **리팩토링**: 분석기 팩이 각자 갖고 있던 실행 결과 판정·경로·JSON 스트림·버전 계열 도우미를 `internal/worker/analysis/pack`으로, 시험 도우미를 `pack/packtest`로 모았다. 한 파일에 여러 일이 섞여 있던 `agent.go`, `personal.go`, `build.go`, `api.go`, `compiledb.go`와 저장 계층의 스케줄러·claim·예약 파일을 같은 패키지 안에서 나눴다. `scripts/validate_bundle.py`는 진입점과 `scripts/bundle_checks/` 모듈로 나눴다. 동작은 바꾸지 않았다(시험 805개 동일, 실제 분석 도구 시험 통과).
- CI가 시험용 브라우저 다운로드를 캐시한다.

### 수정

- `codeye` 도움말의 에이전트 사용법에 빠져 있던 분석기 옵션(`--cppcheck`, `--clang-tidy`, `--eslint`, `--tsc`, `--lizard`, `--jscpd`, `--gitleaks`)을 넣었다.

### 그대로 둔 이름

- 검토 팩 ID `review-r1`·`review-v1`과 그 디렉터리: 저장된 기록이 팩 ID와 digest로 해석된다.
- `migrations/sql/`: 적용된 migration은 checksum으로 대조한다.
- fixture 입력 안의 파일·매크로 이름, 보류 집합 `fixtures/r1-quality/`, 보류 평가 결과 문서: 도구 출력 캡처와 봉인 digest가 그 내용을 고정한다.

## 개인 실용판 — 2026-11-19

한 사람이 한 머신에서 Native로 운영하며 Go, C/C++, Python, JS/TS, Rust 프로젝트를 분석하고 결과를 이력과 함께 보는 범위다. 구현은 2026-11-18에 시작해 2026-11-19에 끝났다.

### 추가

- **모듈·문서·환경 탐색 (#17)**: 고정된 소스에서 모듈별 버전 요구, 노드 관측, 안내 문서를 찾고 일치·불일치·누락 사유를 남긴다. 모델 없이도 목록과 출처가 나온다.
- **프로젝트별 단계 표시 (#17)**: 프로젝트 목록과 실행 상세에서 소스 확보, 탐색, 정적 분석, 코드 검토, 보고서 단계의 상태와 대기 사유를 본다. 늦게 도착한 이전 attempt의 이벤트는 화면을 되돌리지 못한다.
- **예약 실행 (#18)**: cron과 시간대로 정기 분석을 등록한다. 겹침은 합치기·건너뛰기·대기열로 처리하고, 컨트롤러가 멈췄던 동안의 누락은 최신 1건으로 보충한다. 같은 예약 시각은 run을 하나만 만든다.
- **계획 revision과 가중 진행률 (#18)**: 작업이 추가돼도 진행률 분모의 변경 사유가 revision으로 남는다.
- **SSE 실시간 갱신 (#18, #28)**: 순번이 있는 이벤트 스트림, `Last-Event-ID` 재접속, 간극·만료 시 재조회, 사용자별 연결 상한. 마지막 `run.view`는 조회 결과와 같다.
- **분석기 팩 계약 (#21)**: 분석기 서술, 관찰 envelope, 도구별 `required` 여부, 분석기 공백 표시, 규칙 문맥과 소스 안 억제 기록, 공유 toolchain(node). 상세는 `docs/ANALYZERS.md`.
- **저장소 코드 실행 허가 (#21)**: 저장소가 정의한 코드(build script, 설정 등)를 실행하는 분석은 소스 root별 허가가 있을 때만 시작한다. 허가가 없으면 프로세스를 만들지 않고 `repository_code_not_allowed`로 남긴다.
- **분석 팩**: Go Staticcheck (#21), Rust cargo check·Clippy (#22), JS/TS ESLint·tsc (#24), C/C++ clang-tidy·cppcheck (#26), Python Ruff·mypy·pyright·Bandit (#27). 각 팩은 모듈·범위 계획, 도구 버전 계약, 부분 결과와 공백 표시를 갖는다.
- **구조·중복 지표와 비밀정보 (#29)**: lizard 복잡도와 jscpd 중복을 결함이 아닌 지표로 보인다. gitleaks 탐지는 값 없이 위치만 남기고, 탐지된 파일은 모델 입력에서 뺀다.
- **이력과 run 비교 (#20)**: 같은 source variant·profile·scope의 직전 보고서와 비교한다. 줄이 이동해도 같은 발견은 `persisting`이다. 검사가 돌지 않았거나 범위가 줄었거나 도구·규칙 문맥이 바뀌었다면 사라진 발견도 해결이 아니라 `absent_unverified`와 이유로 표시한다.
- **발견에 대한 사람 판정 (#20)**: 이유와 만료 기간을 붙여 발견을 판정한다. 판정은 보고서 게시 시점에 고정되고 viewer는 할 수 없다.
- **품질 평가 runner (#23, #32)**: `scripts/quality_eval.py`와 `scripts/quality_score.py`가 실제 controller·agent·분석기·모델 경로로 사례를 실행하고 채점한다. relay는 요청에 정답 문구가 있으면 전달하지 않고 장부에 남긴다. holdout 등록부는 이미 본 사례를 표시한다. 상세는 `docs/EVALUATION.md`.
- **다섯 언어 검토 입력 (#31)**: 모델 검토용 코드 발췌를 Go뿐 아니라 C/C++, Python, JS/TS, Rust 소스에서도 만든다.
- **개인 모드 `codeye start`·`stop`·`status` (#34)**: 설정 파일 하나로 컨트롤러와 로컬 에이전트를 시작·종료하고 상태를 본다.
- **저장 영역 상한 (#34)**: snapshot·workspace·cache·logs·artifacts별 상한을 넘으면 새 작업을 거절한다. 기존 자료는 지우지 않는다.
- **백업·복원 (#34)**: DB와 확정 산출물을 같은 시점으로 묶어 백업하고, 빈 대상에만 복원한다. 복원 전 무결성을 검증하며 이전 lease를 되살리지 않는다.
- **`codeye doctor --config` (#34)**: 저장 영역 사용량, 최신 백업 나이, start/stop 상태 일관성을 점검한다.
- **사용 안내서 (#34)**: 설치부터 첫 분석, 백업·복원까지의 절차는 `docs/GUIDE.md`.

### 변경

- 검토 지침 팩을 `review-r1` 2.0.0 하나로 합치고 기본값으로 삼았다 (#36). `review-v1`은 이전 기록을 같은 조건으로 재현할 때만 `--review-pack review-v1`로 고른다. 팩을 지정하지 않으면 `review-r1`이다.
- 검토 복구 경로 (#36): JSON이 아닌 모델 응답은 형식 복구와 별도로 한 번 더 요청한다. 검증 역할이 범위 밖 ID만 틀린 항목은 관찰 ID만 제거해 처리하고, 후속 호출이 실패해도 수락 판정을 남긴다.
- 검토 지침은 역할별로 나뉜 후보 팩(#19)에서 출발해 합쳤다. 문서 범위 일반화, 꾸민 근거, 실행·재현 주장은 검증기가 거부한다.
- 모든 checkout의 줄바꿈을 LF로 고정했다 (#19). Windows에서 지침 파일의 digest가 달라지던 문제를 없앤다.
- CI의 Linux 작업 제한 시간을 25분으로 늘렸다.

### 검증한 환경과 범위

- 환경: Ubuntu 26.04 / WSL2 한 호스트(Linux 6.18), Go 1.27.4, Node 24.18.0, PostgreSQL 17.10(임시 Native 인스턴스), Chromium. CI는 Linux와 Windows에서 Go 시험을 실행한다.
- 분석 도구: staticcheck 2026.2.2, cargo/rustc 1.99.1, clippy 0.1.99, ESLint 10.15.0, TypeScript 6.0.3, clang-tidy 23.1.5, cppcheck 2.22.0, gitleaks 8.30.1, lizard 1.26.1, jscpd 5.4.3, Ruff·mypy·pyright·Bandit(버전 계약은 `docs/ANALYZERS.md`). 실제 도구 출력은 캡처로 보관해 CI에서 재생한다.
- 단계 표시, 예약, SSE, 비교와 판정은 실제 PostgreSQL, 제품 워커의 Go vet, Chromium으로 확인했다. 첫 입력은 문서 없는 모듈과 노드보다 높은 버전을 요구하는 모듈이 섞인 합성 저장소다.
- 합성 사례로 기존 검토 팩과 역할별 후보 팩을 같은 모델·같은 입력으로 비교했다. 후보 팩은 같은 결함 포착 수를 더 적은 호출로 냈지만, 조정에 쓴 사례였으므로 독립 평가로 보지 않았다. 이 비교의 승인된 relay 장부 차감은 약 1.50달러(호출 63건)였다.
- 평가 runner는 가짜 upstream으로 경로 전체를 시험했고, 이어 실제 모델로 보류 평가를 한 번 실행했다. 결과는 `docs/EVALUATION-RESULTS.md`.
- 기본 팩 통합 뒤 기준 회귀 사례 12개를 실제 모델로 한 번씩 실행했고, 미리 정한 기준에서 회귀가 없었다.
- 개인 모드는 Linux에서 start·status·stop·백업·복원을 실제 임시 PostgreSQL로 실행했다. Windows 시험 바이너리는 WSL interop으로 실행해 해당 패키지가 통과했다.

### 알려진 한계

- 개인 모드 `start`·`stop`·`status`·`backup`·`restore`는 Linux 전용이다. Windows는 빌드되며 명시적 미지원 오류를 낸다. macOS는 확인하지 않았다.
- RHEL 8.10에서 실행하지 않았다. `pidfd_open`이 없는 커널에서는 `stop`이 신호를 보내지 않고 `recovery_required`로 끝난다.
- Windows에서 실제 분석 도구(staticcheck, cargo, ESLint, clang-tidy 등)를 실행하지 않았다. Go 단위·재생 시험까지만 통과했다. macOS도 같다.
- 데이터베이스는 운영자가 준비한 PostgreSQL만 쓴다. 관리형(번들) PostgreSQL은 없다.
- 백업은 전체 세트만 만든다. 증분, 압축, 암호화, 보존 개수, 자동 예약 백업은 없다. 저장 상한은 새 작업 거절만 하며 오래된 자료를 정리하지 않는다.
- 사용자 판정은 단일 사용자 기준이다. 다중 사용자 권한 격리와 감사는 구현하지 않았다.
- C/C++ compile DB를 만들지 않는다. 없으면 clang-tidy는 미지원, cppcheck는 축소 모드다. Rust 레지스트리 캐시는 지원하지 않아 vendor가 없는 의존성은 `dependency_unavailable`이다. JS/TS는 의존성을 설치하지 않으므로 외부 패키지를 import하는 모듈의 타입 검사는 대부분 공백이다.
- 호출자 확장은 Go 코드에서만 한다. 다른 언어의 파일 간 결함은 발췌에 들어간 범위에서만 보인다.
- 실제 run의 진행률은 작업 수 비율이다. 작업량 가중치를 선언하는 handler가 없다. 예약 수정은 API만 있고 화면에는 등록·삭제만 있다.
- 사람 판정자 간 일치도, 보고서를 읽는 사람의 이해도 평가는 측정하지 않았다. 모델 호출 결과는 실행마다 달라질 수 있어 한 번의 깨끗한 결과가 개선을 보증하지 않는다.
- 새 검토 팩 2.0.0은 독립된 새 사례로 다시 평가하지 않았다.

## 첫 수직 경로 — 2026-11-18

프로젝트를 등록하면 소스를 고정하고 Go vet과 모델 검토를 실행해 근거에 묶인 보고서를 내는 처음부터 끝까지의 경로다. 설계 문서와 진단 기반은 2026-11-17에 올라왔고 나머지는 2026-11-18에 병합됐다.

### 추가

- **환경 진단 `codeye doctor` (#1)**: OS, 도구, 디스크, PostgreSQL 연결과 모델 능력을 검사한다. 버전만 확인한 항목은 `version_only`로 구분하고 비밀값은 출력하지 않는다. Docker가 필요 없다.
- **저장소와 migration (#2)**: 트랜잭션 도메인 모델(run, attempt, task, evidence, lease, 프로젝트 ACL, 이벤트)과 PostgreSQL migration. 명시적 `codeye migrate`로만 스키마가 바뀐다.
- **API·인증·웹 뼈대 (#3)**: loopback 컨트롤러, 일회용 로그인 링크와 세션, Origin·CSRF 검사, 프로젝트 등록, 멱등한 run 요청 202, 취소, 정적 웹 화면.
- **소스 스냅샷 (#4)**: 로컬 디렉터리와 Git에서 분석 입력을 고정한다. manifest와 digest를 남기고 원본은 바꾸지 않으며, 변경 중인 소스와 변조된 스냅샷은 거부한다.
- **Native 실행기 (#5)**: argv·cwd·환경·제한시간으로만 도구를 실행한다. timeout과 취소는 자손까지 종료하고 확인하며, 확인하지 못하면 `recovery_required`로 남는다.
- **노드 에이전트와 워커 프로토콜 (#7)**: 노드 등록, heartbeat, lease 배정, epoch 기반 fencing으로 두 에이전트가 같은 작업을 중복 실행하지 못한다.
- **스케줄러와 자동 증감 (#12)**: 워커 풀의 최소·최대, 0에서 N으로 늘고 다시 0이 되는 증감, drain, CPU·메모리·디스크 예산 입장 제어, lease 만료와 늦은 결과 거부.
- **Go vet 분석 (#8)**: 제품 워커가 오프라인으로 `go vet`을 실행해 위치·해시가 있는 관찰로 정규화한다. 빌드 제약으로 빠진 패키지와 실패를 정상 0건으로 바꾸지 않는다.
- **구조화 보고서와 내보내기 (#13)**: 모델 없이도 작업 상태, 원시 근거, 공백이 보이는 보고서를 게시한다. JSON·Markdown·HTML 내보내기는 기본이 코드 제외이며 같은 입력에서 같은 결과를 낸다.
- **OpenCode 연동 (#9)**: 관리형 OpenCode를 도구 없이, 승인된 모델과 제한된 예산으로 실행한다. 취소하면 프로세스 트리까지 정리하고 잘못된 출력은 게시하지 않는다. 예산 상한이 있는 로컬 relay(`scripts/model_budget_relay.py`)가 호출을 예약·정산한다. 상세는 `docs/REVIEW.md`.
- **맥락·조사·반증 검토 (#10, #11)**: 맥락 수집, 독립 후보 조사, 반례 탐색과 근거 검증. 허위 경로·행·근거 ID와 모델이 단정한 재현·심각도는 거부하거나 미확정으로 남기고, 모델이 실패하면 도구 결과만으로 `partial` 보고서를 낸다.
- **근거 보고서 화면 (#13, #14)**: 요약에서 영역, 후보, 스냅샷 파일과 줄 범위의 근거로 내려가며 반대 근거와 미확정 항목을 함께 본다.
- **회귀 코퍼스와 파일럿 (#15)**: 결함·수정 쌍 10개의 계약 시험(`fixtures/regression/corpus.py --check`)과 실제 프로젝트 파일럿 절차(`scripts/regression_pilot.py`).

### 검증한 환경과 범위

- 환경: Ubuntu 26.04 / WSL2 한 호스트, Go 1.27.4, Node 24.18.0, PostgreSQL 17.10(임시 Native 인스턴스), Git 2.53.0, OpenCode 1.19.5, Chromium. 노드 두 개를 서로 다른 신원으로 같은 호스트에서 실행했다.
- 프로세스 정리는 OS(`/proc`)에서 직접 사망을 확인했다. 두 에이전트 경쟁, lease 만료, 강제 종료 뒤 재시작, 자동 증감은 실제 PostgreSQL과 제품 워커로 시험했다.
- 모델은 승인된 relay를 거친 GPT-6.2 Sol(추론 강도 low)이다. 합성 Go 입력 20개(결함·수정 10쌍), 공개 CODEYE 고정 사본 1개, 공개 HTTPS Git 입력 1개로 파일럿을 실행했다. 22개 보고서가 약 363초에 게시됐다.
- 합성 20개에서 원래 결함 10/10을 포착했고 수정본에서 같은 결함을 다시 지적한 경우는 0/10이었다. 수정본에는 계약 밖 입력에 대한 다른 경고가 일부 나왔다. 이 판정은 개발 에이전트가 정답과 대조한 것이다.
- 비용: 모델 호출 83건이 정산됐고(정상 82, HTTP 오류 1) 보고된 사용량 기준 약 0.99달러, 실패 예약을 포함한 보수적 장부 차감은 약 2.20달러다. 공급자 청구서는 확인하지 않았다.
- 보고서 22개의 주장 38개·근거 27개를 파일 위치·줄 범위·blob SHA로 다시 대조했다. 주장이 옳다는 보증은 아니다.
- Windows는 CI에서 Go 시험을, 로컬에서는 WSL interop으로 시험 바이너리를 실행했다.

### 알려진 한계

- Windows 실제 분석·모델 파일럿, RHEL 8.10, 여러 물리 노드(네트워크 분할, NFS 접근 차이), 철회된 노드 복구는 시험하지 않았다.
- Native 실행은 샌드박스가 아니다. 같은 OS 사용자로 도는 분석 도구는 그 사용자가 읽을 수 있는 파일을 읽는다. 메모리·CPU 한도는 입장 제어용이며 OS 강제 한도가 아니다.
- 프로세스 그룹을 벗어난 자손(`setsid`)은 추적하지 못해 `uncertain`으로 남는다. `pidfd` 그룹 신호가 없는 Linux 6.9 미만 커널에서는 일부 강제 종료가 막힌다.
- 비공개 Git 인증, 사내 CA·proxy, GHES, submodule·LFS 객체 다운로드는 구현하지 않았다.
- 분석 범위는 Go vet 하나, 노드 기본 GOOS/GOARCH와 기본 build tag, cgo 제외다. 모듈 캐시는 운영자가 준비한다.
- 파일럿의 모델 검토 표본은 작고 합성 위주다. 사람 검토자의 precision, 보고서를 읽는 사람의 이해도, 다른 언어의 탐지율은 측정하지 않았다. 도구 진단 수나 시험 통과를 분석 품질로 해석하지 않는다.
- artifact 파일은 DB 기록 전에 저장되므로 기록 실패 시 참조 없는 파일이 남을 수 있다(정리 기능 없음).
