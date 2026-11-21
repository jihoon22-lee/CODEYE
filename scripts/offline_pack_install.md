# CODEYE {{version}} 설치 ({{os}}/{{arch}})

이 묶음에는 `codeye` 실행 파일, 빌드된 웹 화면, 사용·운영 문서, 오프라인 설정 예시가 있다. Linux 일반 계정으로 실행한다. 기본 설치에는 PostgreSQL 서버 프로그램 14 이상이 필요하다. CODEYE가 전용 데이터베이스를 초기화하고 시작한다. 프로젝트 언어별 분석 도구, `git`, OpenCode는 필요한 경우에 따로 준비한다. 자세한 준비물은 묶음의 `docs/GUIDE.md`에 있다.

## 파일 확인

묶음과 옆의 SHA-256 검증 파일을 같은 폴더에 둔다. 인터넷 없는 머신으로 옮길 때도 두 파일을 함께 복사한다.

```sh
sha256sum -c {{top}}.tar.gz.sha256 &&
  tar -xzf {{top}}.tar.gz &&
  cd {{top}} &&
  sha256sum -c SHA256SUMS
```

풀어 놓은 디렉터리를 설치 위치에 그대로 둔다. `codeye`는 옆의 `web/dist`를 웹 화면으로 쓴다. `./codeye version`에서 버전과 커밋을 확인할 수 있다.

## 실행

```sh
./codeye up
```

브라우저가 열리지 않으면 터미널에 나온 관리자 로그인 링크를 연다. 첫 화면의 **폴더 경로 또는 Git 주소**에 값을 넣고 **분석**을 누른다. PostgreSQL을 찾지 못하면 `./codeye up --postgres-bin-dir /절대/경로/bin`으로 위치를 지정한다.

## 인터넷 없는 머신

설치할 머신에 적용할 미러·사내 CA·모델 주소가 있다면 `examples/config-offline.json`을 `~/.config/codeye/config.json`으로 복사해 쓰는 주소·경로만 남긴다. `--offline`은 설정에 저장된다.

```sh
./codeye up --offline
./codeye doctor --config ~/.config/codeye/config.json
./codeye tools --checklist
```

`doctor`의 **Offline readiness**에서 미러, CA, PostgreSQL을 확인한다. `tools --checklist`는 추가할 분석 도구를 보여 준다. 미러와 인증서 준비 절차는 묶음의 `docs/OPERATIONS.md` 중 **폐쇄망 배포팩**에 있다.
