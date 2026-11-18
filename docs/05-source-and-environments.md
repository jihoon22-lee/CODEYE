# 05. 소스 스냅샷과 프로젝트 환경

R0의 소스 취득과 재시도 기준은 [스냅샷 계약](25-w04-source.md)에 있다. W15에서는 그 manifest와 노드 관측값으로 모듈별 환경을 산출한다. 아래의 의존성 설치·다언어 준비는 해당 프로필과 실행 권한을 구현한 뒤의 동작이다.

## 1. 등록과 접근

Git 등록은 provider ID·repository ID/URL·branch를 저장한다. 로컬 등록은 node 또는 source-root ID와 그 아래 상대 경로를 저장한다. 브라우저 PC의 경로와 서버의 경로를 혼동하지 않도록 선택한 노드를 표시한다. NFS의 동일 경로가 모든 노드에서 같은 권한으로 보인다고 가정하지 않는다.

운영자는 허용 source root를 등록하고 프로젝트 관리자는 그 하위 경로만 선택한다. 경로 정규화·실제 경로 확인 후 심볼릭 링크·Windows junction·UNC 경계를 검사한다. 사용자 인증과 OS 접근 검사는 둘 다 통과해야 한다. 디렉터리 탐색 API도 같은 ACL을 적용한다.

## 2. Git 입력 고정

1. 승인된 credential로 저장소와 선택 브랜치 존재를 확인한다.
2. 실제 acquire 단계에서 fetch하고 관찰한 HEAD SHA를 고정한다.
3. 브랜치명·요청 시각·확정 시각·SHA·provider repository ID를 기록한다.
4. detached 작업 사본을 만들고 credential·remote URL 내 토큰은 작업 사본에 남기지 않는다.
5. manifest를 확정하고 snapshot ID를 run에 한 번만 연결한다.

대기 중 새 커밋이 생기면 소스 확보 시점의 커밋을 사용한다. 고정 이후에는 브랜치 이동·force push가 영향을 주지 않는다. PR 실행은 서버가 확인한 head/base SHA를 사용하며 자동으로 최신 HEAD로 바꾸지 않는다. 머지 결과 분석은 별도 프로필로 명시하지 않는 한 수행하지 않는다.

submodule과 LFS는 기본 진단 대상이다. 필요한 객체를 가져오지 못하면 pointer만 분석하고 실제 소스를 분석했다고 하지 않는다. 다른 호스트·저장소의 submodule은 해당 접근 정책을 다시 검사한다. hooks·checkout filter·Git 설정은 승인된 설정만 사용한다. bare cache는 credential scope별로 분리하고 임의 repo hook을 실행하지 않는다.

## 3. 로컬/NFS 입력

로컬은 커밋 여부와 무관하게 선택한 파일을 복사한다. 기본적으로 코드·문서·설정·lockfile을 포함하고 .git 내부·대형 캐시·운영 비밀파일·빌드 산출물은 정책에 따라 제외한다. 사용자는 생성 헤더나 vendored code 등 필요한 예외를 추가할 수 있다. 제외 파일 수·이유·적용 규칙을 남긴다.

복사는 원자적 스냅샷이 아니다. 각 파일을 읽기 전후 stat하고 읽은 바이트의 hash를 계산한다. 복사 완료 후 파일 집합·크기·mtime 변화를 재검사한다. 변경 감지 시 제한된 재시도(기본 2회)를 하고 계속 변하면 source_unstable로 종료한다. 시간·크기 비교만으로 모든 동시 변경을 증명할 수 없으므로 consistency는 stable_copy_best_effort로 기록한다. 파일시스템 snapshot이 승인되어 있으면 별도 강한 일관성 어댑터를 사용할 수 있다.

외부 링크는 기본적으로 따라가지 않고 목록과 이유만 보존한다. 필요한 외부 입력은 별도 승인 root로 등록하여 명시적으로 복사한다. device·socket·FIFO 등은 건너뛴다. 대소문자 충돌·Windows 예약 파일명·인코딩 문제는 실패/미지원으로 드러낸다. 아카이브 추출은 경로 탈출·링크·확장 용량 제한을 적용한다.

## 4. 저장 구조

```text
runs/<run_id>/<attempt_id>/
  snapshot/       # 확정 입력; evidence 기준
  workspace/      # install/build/test/분석용 사본
  tools/          # 실행별 wrapper·설정
  temp/           # 임시 파일
  outputs/        # 원시 결과·생성 artifact
  logs/           # 제한된 로컬 spool
```

snapshot의 불변성은 플랫폼의 쓰기 정책과 hash 확인으로 유지한다. 같은 OS 계정에서 실행되는 적대적 프로세스를 막는 보안 경계라고 표현하지 않는다. Native 신뢰 수준은 [실행 명세](08-native-execution.md)를 따른다. snapshot과 workspace를 hard link로 공유하지 않는다. reflink/CoW는 실제 분리 동작을 검증한 경우만 최적화로 사용한다.

manifest에는 상대 경로·종류·byte size·SHA-256·mode·encoding 힌트·제외 이유를 담는다. 출력 위치는 snapshot 상대 경로로 변환하고 생성 파일은 artifact URI로 별도 표기한다. 원본 NFS 절대 경로는 보고서에 기본 노출하지 않는다.

## 5. 환경 탐색

| 언어 | 주 입력 | 추가 조건 |
|---|---|---|
| C/C++ | CMake/Make/Meson 설정, compile_commands.json | compiler·target·표준·include·define·생성 헤더 |
| Python | pyproject, requirements/lock, 환경·버전 파일 | interpreter·가상환경·stub·import resolution |
| JS/TS | package.json, lockfile, tsconfig, workspace | Node·package manager·project references·ESM/CJS |
| Go | go.mod/go.sum/go.work | Go toolchain·tags·CGO·GOOS/GOARCH |
| Rust | Cargo.toml/lock, rust-toolchain | compiler·target·features·build.rs·proc-macro |

프로젝트 요구 환경, 확보한 환경, 차이, 분석 영향은 각각 저장한다. 모노레포는 모듈별로 다른 언어/버전을 허용한다. 같은 프로젝트의 두 버전 환경을 전역 PATH 수정으로 충돌시키지 않는다.

## 6. 환경 준비 정책

우선순위는 명시된 프로젝트 제약→lockfile→검출된 호환 런타임→운영자 카탈로그다. 적합한 버전이 없으면 임의 최신 버전으로 대체하지 않는다. 버전 불일치에서도 가능한 텍스트 검사는 계속하되 의미 분석/테스트의 유효 범위를 표시한다.

분석 도구 자체의 런타임과 대상 프로젝트의 런타임을 분리한다. 도구는 별도 venv/패키지 디렉터리에 고정하고 프로젝트 의존성과 섞지 않는다. 설치는 승인된 카탈로그·미러·해시를 사용하며 lockfile을 갱신하지 않는 모드를 우선한다. 설치 도중 스크립트 실행 여부는 실행 권한에 포함한다.

Go의 toolchain·module 다운로드, Rust toolchain·crate 접근, Node lifecycle scripts, Python build backend는 네트워크/코드 실행이 생길 수 있는 단계로 취급한다. 폐쇄망에서 실패하면 외부로 우회하지 않는다. 내부 CA·proxy·registry는 플랫폼 배포 설정에서 주입하고 저장소 설정이 이를 대체하지 못하게 한다.

## 7. C/C++ 특칙

compile_commands의 directory/file/include 경로를 원본→snapshot/workspace 매핑으로 변환한다. 허용 root 밖의 헤더는 명시적 read-only 입력으로 승인되었는지 확인한다. 원본 command 문자열을 그대로 실행하지 않고 인자 배열·toolchain·플래그를 검증한다.

컴파일 DB가 없으면 승인된 configure/build에서 생성·수집할 수 있다. 이 단계는 코드 실행이며 Native 권한이 필요하다. 전체 translation unit을 분석했는지, 일부 build variant만 분석했는지 보고한다. 코드 복잡도와 텍스트 검사만 가능한 상태를 'C++ 정밀 분석 완료'로 표시하지 않는다.

## 8. 재실행

새로 분석은 새 입력을 고정한다. 같은 입력 재실행은 보관된 snapshot·설정 revision을 사용한다. 실패 단계 재시도는 선행 산출물의 해시·환경이 일치할 때만 재사용한다. 입력이 삭제됐거나 접근 권한이 철회되면 이전 캐시로 권한을 우회하지 않는다.
