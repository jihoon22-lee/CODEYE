# W01 환경 진단 계약과 검증 기록

`codeye doctor`는 호스트·도구·디스크·DB를 검사한다. 실제 모델 요청은 [모델 probe](32-w10-opencode.md), 프로세스 트리 종료는 [Native 실행기 시험](26-w05-native.md)으로 확인한다. R0 전체의 실행 순서와 한계는 [파일럿 기록](36-r0-pilot.md)에 있다.

## 진단 실행

```bash
go build -o bin/codeye ./cmd/codeye
./bin/codeye doctor --format json
```

## 출력과 종료 코드

```text
codeye doctor [--format text|json] [--work-dir .codeye/work] [--timeout 5s] [--config PATH]
```

- [doctor Schema](../schemas/doctor.schema.json), [합성 예시](../examples/doctor.json). 예시는 현재 머신의 실측 결과가 아니다.
- `schema_version=0.1`, `stage=foundation`. host에 OS/arch/논리 CPU·가능한 RAM/디스크 관측치를 기록한다. Linux 외 RAM·디스크 관측 미구현은 null이며 자원 한도 적용을 의미하지 않는다.
- `available`: 해당 probe 범위 확인. `unavailable`: 실패/실행 불가. `not_configured`: DB 설정 없음. `unverified`: 계약·능력 미검증.
- `version_only`는 버전 문자열만 확인했다는 뜻이다. 언어 어댑터 지원·분석 품질·OS 호환성 인증이 아니다. Python은 `python3`, C/C++는 `cc`를 탐색한다. 다른 실행명·MSVC·별도 toolchain profile 탐지는 후속이다.
- 종료 코드 0은 모든 진단 항목 확인, 1은 사용 불가·미설정·미검증 항목 존재, 2는 사용법/출력 오류다. 버전만 확인한 항목이 있으면 진단 자체를 정상 수행해도 1을 반환할 수 있다. 이는 분석 run 상태와 별개다.
- `--timeout`은 probe별 제한이며 기본 5초, 최대 1분이다. 여러 probe를 순서대로 실행하므로 전체 시간 제한은 아니다.
- 작업 디스크 probe는 지정한 플랫폼 work 경로를 생성하고 임시 파일 쓰기→sync→읽기→삭제를 확인한다. 분석 대상 프로젝트 경로를 지정하지 않는다. 같은 경로의 기존 파일을 덮어쓰지 않는다.
- 도구는 PATH에서 찾은 실행 파일을 argv로 호출한다. 임시 cwd/home, 제한된 환경, 8 KiB 출력 상한을 사용한다. 계정 credential을 상속하지 않는다. 실행 파일 자체는 운영자가 신뢰하는 설치여야 한다. 이는 OS 샌드박스가 아니다.
- W25: `--config PATH`(개인 모드 설정)를 주면 선택 필드 `operations`가 붙는다. 저장 영역별 사용량·상한·파일시스템 여유, 최신 백업 나이, start/stop 상태 일관성이다. 종료 코드 규칙은 같다. 상세는 [W25 기록](47-w25-packaging-and-operations.md) §6.
- 원시 stdout/stderr·실행 경로·DSN·비밀번호를 결과에 넣지 않는다. 버전은 숫자 부분만 기록하고 실패는 고정 reason으로 표시한다. timeout은 종료를 확정하지 않고 `recovery_required`다. Linux의 probe process group 중지는 W05 전체 취소 계약을 대체하지 않는다.

DB는 `CODEYE_DATABASE_URL` 환경변수에 명시한 PostgreSQL URL만 검사한다. argv에 URL을 넣는 옵션은 없다. URL에는 host/user/database가 필요하며 `sslmode`, `sslrootcert`만 허용한다. 기본 TLS는 verify-full, `sslmode=disable`은 `127.0.0.1`/`::1`만 허용한다. 인증정보는 psql 자식 환경에만 전달한다. 사용자 psqlrc·PGSERVICE·기존 PGPASS·운영 DB 환경을 상속하지 않는다. `psql -X -w`로 비대화형 읽기 쿼리 `SELECT current_setting('server_version')`만 수행한다. psql이 없으면 명확히 실패한다. TCP 포트가 열렸다는 이유로 DB 정상이라고 표시하지 않는다. DB 생성·migration은 하지 않는다.

## W01에서 실행한 시험

당시 환경은 Ubuntu 26.04.1 / WSL2, Go 1.27.4, Node 24.18.0, PostgreSQL 17.10이었다. TC-36~39는 진단과 파일 뷰어 범위를 검증했다.

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

### 최초 CI

커밋 `93a56c1`의 [검증 실행](https://github.com/jihoon22-lee/CODEYE/actions/runs/44105863377)에서 아래 조합을 확인했다.

- Ubuntu 24.04 / Go 1.27.4 / PostgreSQL 16.15: Native 임시 DB·race·vet·빌드·문서 계약·기존 fixture 통과. 로컬 PostgreSQL 17.10 결과와 별도로 기록한다.
- Windows Server 2025 / Go 1.27.4: 실제 Go 단위·자식 프로세스 probe·vet·빌드 통과. DB 통합 시험 두 개는 설정이 없어 명시적으로 skip했다. Windows 11 NativeExecutor/Job Object 수용 시험은 미실행이다.
- Node 24.18.0: lockfile 설치, 웹 테스트 10개, TypeScript·정적 빌드 통과.
