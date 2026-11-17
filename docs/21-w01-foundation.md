# 21. W01 실행 기반과 검증 기록

상태: W01 구현 · 2026-11-17. R0 분석 플랫폼 완료가 아니다. 이 문서는 기존 00~20 설계, AGENTS, README, INDEX 및 계약을 검토한 뒤 확정한 첫 작업의 범위와 실행 증거다.

## 1. 검토 결과와 범위

착수를 막는 확정 요구의 충돌은 발견하지 않았다. W01→W02→W03 순서와 W01 이후 W05 분기는 유지한다. 첫 작업에서 프로젝트/run API를 만들면 W02의 ACL·영속 상태·lease 선행조건을 건너뛰므로 이번 범위에 넣지 않는다.

보완한 공백은 다음과 같다.

- W01 doctor의 구체적인 결과 계약과 대응 TC가 없었다. CLI 전용 Schema와 TC-36~39를 추가했다.
- `start`는 controller와 agent를 함께 기동하는 계약이다. 정적 웹 미리보기를 `start` 구현으로 표시하지 않는다.
- 도구 버전 확인과 실행 능력 검증은 다르다. OpenCode CLI가 있어도 workspace·모델·취소·permission 계약은 W10 전까지 `unverified`다. Native 프로세스 트리 제어는 W05 전까지 `unverified`다.
- 기존 문서 validator가 저장소 전체의 Markdown을 순회하여 설치된 의존성 문서까지 검사하게 되는 문제를 보완했다. `.git`, `.codeye`, `.venv`, `node_modules`, `dist`, `bin`, `coverage`, `__pycache__`를 제외한다.

요구 연결: REQ-004/026의 진단 기반, REQ-030의 무자동 다운로드·무모델 fallback, REQ-032/033 범위 유지, NFR-001/006의 실제 환경 기록·비밀값 비노출. 자동 프로젝트 탐색, 모델 계약, Native 작업 실행 전체를 충족했다는 의미는 아니다. 모델·분석 지침과 품질 평가 자료는 변경하지 않았다.

## 2. 개발 환경과 실행

이번 검증 환경은 Ubuntu 26.04.1 / WSL2 / Linux amd64, Go 1.27.4, Node.js 24.18.0, npm 11.16.0, Python 3.14.4, PostgreSQL 17.10이다. WSL에서 성공한 결과는 Windows Native 실행 증거가 아니다.

Go는 [공식 다운로드](https://go.dev/dl/)의 SHA-256을 확인해 사용자 전용 경로에 설치했다. CODEYE 자체 개발에는 Go 1.27 이상과 Node 22.12 이상을 사용한다. 현재 시험한 버전은 위 조합뿐이다. 분석 대상 프로젝트의 Go/Node 버전을 강제하는 조건은 아니다. React/TypeScript/Vite 등은 [package.json](../web/package.json)과 [package-lock.json](../web/package-lock.json)에 고정한다. Go 코드는 현재 표준 라이브러리만 사용한다.

저장소 루트에서:

```bash
go build -o bin/codeye ./cmd/codeye
./bin/codeye doctor
./bin/codeye doctor --format json > .codeye/doctor.json

npm --prefix web ci
npm --prefix web run dev
```

웹 주소는 터미널에 표시된다(기본 `http://127.0.0.1:5173`). `doctor` 파일을 선택하면 로컬 브라우저에서 Schema 검증 후 표시한다. 파일은 업로드·영속 저장하지 않는다. 웹에 분석 요청이나 가짜 결과는 없다. UI 문자열은 `web/src/locales/ko.ts`로 분리한다.

정적 배포 파일 생성·미리보기:

```bash
npm --prefix web run build
npm --prefix web run preview
```

`web/dist`는 로컬 JS/CSS만 포함하며 외부 CDN·모델·서비스에 요청하지 않는다. Vite 서버는 개발용이고 운영용 controller가 아니다. `codeye start`, `stop`, `agent`와 HTTP API는 아직 설계 계약이다. `examples/platform.yaml`도 아직 CLI가 읽는 설정이 아니다.

패키지 설치는 개발자가 명시적으로 실행하는 준비 단계다. `doctor`는 설치·업데이트·분석기 다운로드·모델 요청을 하지 않는다. 폐쇄망 개발은 미리 준비한 도구와 승인된 패키지 미러/캐시를 사용한다.

## 3. Doctor 계약

```text
codeye doctor [--format text|json] [--work-dir .codeye/work] [--timeout 5s]
```

- [doctor Schema](../schemas/doctor.schema.json), [합성 예시](../examples/doctor.json). 예시는 현재 머신의 실측 결과가 아니다.
- `schema_version=0.1`, `stage=foundation`. host에 OS/arch/논리 CPU·가능한 RAM/디스크 관측치를 기록한다. Linux 외 RAM·디스크 관측 미구현은 null이며 자원 한도 적용을 의미하지 않는다.
- `available`: 해당 probe 범위 확인. `unavailable`: 실패/실행 불가. `not_configured`: DB 설정 없음. `unverified`: 계약·능력 미검증.
- `version_only`는 버전 문자열만 확인했다는 뜻이다. 언어 어댑터 지원·분석 품질·OS 호환성 인증이 아니다. Python은 `python3`, C/C++는 `cc`를 탐색한다. 다른 실행명·MSVC·별도 toolchain profile 탐지는 후속이다.
- 종료 코드 0은 모든 진단 항목 확인, 1은 사용 불가·미설정·미검증 항목 존재, 2는 사용법/출력 오류다. W01에는 W05/W10 미검증 항목이 있으므로 정상적인 진단 실행도 1을 반환한다. 이는 분석 run 상태와 별개다.
- `--timeout`은 probe별 제한이며 기본 5초, 최대 1분이다. 여러 probe를 순서대로 실행하므로 전체 시간 제한은 아니다.
- 작업 디스크 probe는 지정한 플랫폼 work 경로를 생성하고 임시 파일 쓰기→sync→읽기→삭제를 확인한다. 분석 대상 프로젝트 경로를 지정하지 않는다. 같은 경로의 기존 파일을 덮어쓰지 않는다.
- 도구는 PATH에서 찾은 실행 파일을 argv로 호출한다. 임시 cwd/home, 제한된 환경, 8 KiB 출력 상한을 사용한다. 계정 credential을 상속하지 않는다. 실행 파일 자체는 운영자가 신뢰하는 설치여야 한다. 이는 OS 샌드박스가 아니다.
- 원시 stdout/stderr·실행 경로·DSN·비밀번호를 결과에 넣지 않는다. 버전은 숫자 부분만 기록하고 실패는 고정 reason으로 표시한다. timeout은 종료를 확정하지 않고 `recovery_required`다. Linux의 probe process group 중지는 W05 전체 취소 계약을 대체하지 않는다.

DB는 `CODEYE_DATABASE_URL` 환경변수에 명시한 PostgreSQL URL만 검사한다. argv에 URL을 넣는 옵션은 없다. URL에는 host/user/database가 필요하며 `sslmode`, `sslrootcert`만 허용한다. 기본 TLS는 verify-full, `sslmode=disable`은 `127.0.0.1`/`::1`만 허용한다. 인증정보는 psql 자식 환경에만 전달한다. 사용자 psqlrc·PGSERVICE·기존 PGPASS·운영 DB 환경을 상속하지 않는다. `psql -X -w`로 비대화형 읽기 쿼리 `SELECT current_setting('server_version')`만 수행한다. psql이 없으면 명확히 실패한다. TCP 포트가 열렸다는 이유로 DB 정상이라고 표시하지 않는다. DB 생성·migration은 하지 않는다.

현재 PC의 OpenCode 무료 모델 한도를 고려해 W01에서는 `opencode --version`만 실행한다. 모델 요청은 0회다. 기본 어댑터는 OpenCode이며 Codex/Claude Code는 후속의 명시적 선택 후보다. 연결 계약이 같다고 가정하지 않으며 자동 fallback하지 않는다.

## 4. 자동화 검증

```bash
go test ./...
go test -race ./...
go vet ./...
npm --prefix web test
npm --prefix web run build
python3 scripts/validate_bundle.py
python3 -m unittest discover -s fixtures/cleanup -p 'test_*.py' -v
```

DB 통합 시험에는 Native PostgreSQL의 `initdb`, `postgres`, `psql`을 PATH에 둔다. 일반 사용자로 실행하며 Python의 `scripts/requirements.txt` 의존성이 필요하다.

```bash
python3 scripts/test_postgres.py
```

이 스크립트는 임시 데이터 디렉터리·임의 포트·무작위 테스트 비밀번호로 loopback PostgreSQL을 기동한다. 기존 DB/서비스에 접속하거나 설정을 변경하지 않는다. 테스트가 끝나면 해당 프로세스를 종료하고 임시 파일을 제거한다. Windows용 시험 실행 스크립트는 아직 없다. 통합 환경 없이 `go test`만 실행하면 DB 시험 두 개는 명시적으로 skip한다.

| 검증 | 실제 결과 |
|---|---|
| Go 두 패키지 단위·실제 자식 프로세스·race 검사 | 통과. 제한시간/출력 cap/환경 비밀값 미상속/임시 cwd/옵션 오류/진단 실패·JSON 확인 |
| Native PostgreSQL 통합 | 통과. 인증 query, 비밀번호 오류, DB 없음, CLI JSON Schema와 비밀값 미노출 |
| Go vet 및 Linux 바이너리 빌드 | 통과 |
| Windows amd64 교차 빌드 | 통과. Windows에서 실행한 결과 아님 |
| 웹 자동화 10개 | 통과. Schema·미구현 표시·로컬 파일 import·잘못된 파일·오래된 비동기 결과 무시·무네트워크 |
| TypeScript 검사·Vite 정적 빌드 | 통과 |
| loopback 정적 preview HTTP smoke | 통과. HTML 및 로컬 JS/CSS 응답 확인, 실제 브라우저 시험은 아님 |
| 문서·Schema·예시 보조 validator | 통과. 제품 분석 검증과 별개 |
| 기존 cleanup fixture 6개 | 통과. CODEYE가 결함을 찾았다는 뜻이 아님 |

실제 기본 doctor에서는 Git/Go/Node/Python/cc/OpenCode CLI 버전이 확인되었다. Rust probe는 격리된 환경에서 실패했고 기본 PATH에는 psql이 없었다. DB 설정이 없으면 `not_configured`로 표시한다. 통합 시험에서만 별도로 준비한 PostgreSQL PATH와 임시 DB를 사용했다. 사용자 계정 설정을 무조건 상속해 probe를 통과시키지 않는다.

## 5. 미실행·남은 제한

Windows 11·RHEL 8.10의 제품 실행, 브라우저 실기/접근성·성능 측정, 실제 OpenCode 모델/workspace/취소 계약, 분석 품질 평가, TC-01~34 전체 수직 시험은 미실행이다. NFR-001과 REQ-026 전체 완료를 주장하지 않는다.

W02의 영속 도메인/ACL/event/lease 트랜잭션이 다음 순서다. 이후 W03 API·인증, W04 Git/local snapshot, W05 NativeExecutor, W06~14의 워커·실도구·모델·보고서 수직 기능이 남는다. PostgreSQL 진단에 쓰는 psql은 W02 저장 계층 구현 선택을 강제하지 않는다. 이 작업에는 DB migration이 없으며 롤백은 브랜치 변경과 로컬 빌드 산출물 제거만 필요하다.


## 6. PR CI와 검토 (2026-11-17)

PR에 기존 CI가 없어 CODEYE 저장소 자체의 [검증 workflow](../.github/workflows/verify.yml)를 추가했다. 제품 범위의 CI 비목표 해석은 결정 A13을 따른다. [첫 실행](https://github.com/jihoon22-lee/CODEYE/actions/runs/44105863377)의 대상 head는 `93a56c1`이며 세 job 모두 통과했다.

- Ubuntu 24.04 / Go 1.27.4 / PostgreSQL 16.15: Native 임시 DB·race·vet·빌드·문서 계약·기존 fixture 통과. 로컬 PostgreSQL 17.10 결과와 별도로 기록한다.
- Windows Server 2025 / Go 1.27.4: 실제 Go 단위·자식 프로세스 probe·vet·빌드 통과. DB 통합 시험 두 개는 설정이 없어 명시적으로 skip했다. Windows 11 NativeExecutor/Job Object 수용 시험은 미실행이다.
- Node 24.18.0: lockfile 설치, 웹 테스트 10개, TypeScript·정적 빌드 통과.

코드 검토는 CLI 입력/비밀값 노출, DB TLS·인증·실패 구분, probe 환경/출력/timeout, JSON Schema와 웹 비동기 파일 교체, 설계·시험 범위를 확인했다. W01 범위에서 병합 차단 결함은 발견하지 못했다. 이는 별도 독립 검토자의 승인이나 분석 품질 보증이 아니다.

W05 프로세스 트리 종료와 W10 모델 능력은 계속 미검증이다. CI에도 모델을 연결하지 않는다. [W02 준비 계획](22-w02-preparation.md)에 다음 작업 순서·시험·미확정 계약을 정리했으며 W02 구현은 아직 시작하지 않았다.
