# CODEYE {{version}} 설치 ({{os}}/{{arch}})

이 묶음에는 `codeye` 실행 파일, 빌드된 웹 화면, 사용·운영 문서, 오프라인 설정 예시가 있다. 배포판의 아키텍처를 확인하고, 분석할 폴더를 읽을 수 있는 Linux 일반 계정으로 실행한다. 기본 설치에는 PostgreSQL 서버 프로그램 14 이상이 필요하다. CODEYE가 전용 데이터베이스를 초기화하고 시작한다. 프로젝트별 분석 도구, `git`, OpenCode는 필요에 따라 따로 준비한다. [준비물과 설치](../docs/GUIDE.md#첫-시작)

## 파일 확인

묶음과 옆의 SHA-256 검증 파일을 같은 폴더에 둔다. 인터넷 없는 머신으로 옮길 때도 두 파일을 함께 복사한다. 배포판마다 별도 디렉터리에 푼다.

```sh
sha256sum -c {{top}}.tar.gz.sha256 &&
  tar --keep-old-files -xzf {{top}}.tar.gz &&
  cd {{top}} &&
  sha256sum -c SHA256SUMS
```

풀어 놓은 실행 파일과 옆의 `web/dist`를 함께 둔다. `./codeye version`에서 버전과 커밋을 확인할 수 있다. 이번 배포의 변경 사항은 묶음의 [변경 이력](../CHANGELOG.md)에 있다. 기존 설치가 있으면 시작하기 전에 [배포판 업그레이드](../docs/OPERATIONS.md#배포판-업그레이드)에서 정지·백업·설정 유지 절차를 따른다.

## 실행

```sh
./codeye up
```

브라우저가 열리지 않으면 터미널에 나온 관리자 로그인 링크를 연다. 첫 화면의 **폴더 경로 또는 Git 주소**에 값을 넣고 **분석**을 누른다. 다시 들어갈 때는 `./codeye open`으로 관리자 링크를 발급받을 수 있다. 다른 사용자는 서비스 주소의 가입 화면에서 이메일과 비밀번호로 계정을 만든다. 이메일 계정으로 관리하려면 최초 관리자 화면의 **관리 > 사용자**에서 해당 이메일의 **관리자로 지정**을 누른다.

PostgreSQL을 찾지 못하면 `./codeye up --postgres-bin-dir /절대/경로/bin`으로 위치를 지정한다. 설정은 기본적으로 `~/.config/codeye/config.json`, 관리형 DB와 분석 데이터는 `~/.local/state/codeye`에 저장된다. [서비스 설정 파일](../docs/OPERATIONS.md#서비스-설정-파일)

## 분석 도구 확인

`./codeye tools --checklist`로 이 머신에서 사용 가능한 분석기를 확인한다. **관리 > 언어 도구 경로**에서 이 머신의 Python, JDK, Go, Rust(Cargo), Node.js 실행 파일이나 설치 디렉터리를 지정할 수 있다. 다음 로컬 분석부터 적용된다. [언어 도구 설정](../docs/GUIDE.md#언어-도구-경로)

Java 프로젝트는 JDK와 PMD를 준비하고 바이트코드 검사에는 SpotBugs를 추가한다. **테스트 포함 분석**은 해당 언어의 빌드 도구와 테스트 의존성을 사용한다. [언어별 분석과 테스트](../docs/GUIDE.md#언어별-분석과-테스트)

## 인터넷 없는 머신

오프라인으로 설치할 때는 처음부터 아래 명령으로 실행한다. `--offline`은 설정에 저장된다. 미러·사내 CA·모델 주소가 필요하면 `examples/config-offline.json`을 참고해 새 설정 파일을 준비하거나 기존 설정에서 필요한 항목을 편집한다. 예시의 주소와 경로는 설치할 머신에 맞춘다.

```sh
config="${XDG_CONFIG_HOME:-$HOME/.config}/codeye/config.json"
./codeye up --config "$config" --offline
./codeye doctor --config "$config"
./codeye tools --config "$config" --checklist
```

`doctor`의 **Offline readiness**에서 미러, CA, PostgreSQL을 확인한다. `tools --checklist`는 추가할 분석 도구를 보여 준다. [폐쇄망 배포팩](../docs/OPERATIONS.md#폐쇄망-배포팩)
