# W18 JS/TS fixture

CODEYE W18 수용 시험(TC-111~TC-113)의 입력이다. 결함은 의도된 것이다.

- `packages/app`: TypeScript 모듈. `tsconfig.json`(JSONC, 상위 `extends`, 언어 서비스 plugin 항목)과 비동기·경계 결함, 억제 지시문, tsconfig 밖 파일(`tools/gen.ts`)을 가진다.
- `packages/lib`: JavaScript 전용 모듈. 실행되면 표식 파일을 쓰는 저장소 `eslint.config.js`를 가진다.
- `scripts/release.mjs`: package.json 모듈 밖의 루트 소스.

`node_modules`는 없다. 스냅샷은 `node_modules`를 항상 제외한다.
