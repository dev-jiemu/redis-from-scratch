# redis-from-scratch

Redis를 Go로 직접 구현하며 내부 동작을 이해하는 학습 프로젝트.

- 호환 목표: `redis-cli`, `redis-benchmark` 접속 (RESP2)

## 구조

설계의 5개 레이어와 패키지가 1:1로 대응한다. 의존 방향은 `network → protocol → command → store → persist` 단방향.

| 패키지 | 역할 |
|--------|------|
| `cmd/server` | 서버 진입점 |
| `internal/network` | TCP 서버, (추후) event loop |
| `internal/protocol` | RESP2 parser / writer |
| `internal/command` | command table, dispatcher |
| `internal/store` | DB, object 타입, expiry |
| `internal/persist` | RDB, AOF |

## 개발

```
make build   # go build ./...
make test    # go test -race ./...
make bench   # go test -bench=. -benchmem ./...
```

## 진행 현황

| Phase | 작업 | 상태 |
|-------|------|------|
| 0 | 프로젝트 셋업 (모듈, 패키지 뼈대, Makefile) | ✅ 완료 |
| 1-1 | RESP2 파서 (부분 수신 처리, 프로토콜 에러 구분) | ✅ 완료 |
| 1-2 | RESP writer (parser와 round-trip 검증) | ✅ 완료 |
| 1-3 | TCP 서버 (conn당 goroutine) + 단일 executor | ✅ 완료 |
| 1-4 ~ 1-5 | command table / dispatcher, in-memory store | ⏭️ 다음 작업 |
| 2 ~ 7 | 자료구조, TTL, RDB/AOF, 심화 기능, event loop 교체, 인코딩 최적화 | ⏳ 예정 |

### 다음에 이어서 할 작업

**1-4 command table / dispatcher** (`PING`, `ECHO`, `GET`, `SET`, `DEL`, `EXISTS`, 알 수 없는 명령은 `-ERR unknown command`)

- **소견**: 1-5(in-memory `map[string]*Object`)와 **같이 진행**하는 것을 권장한다. `GET`/`SET`/`DEL`/`EXISTS`는 store 없이는 의미 있는 테스트를 쓸 수 없고, dispatcher가 store 인터페이스를 어떻게 쓰는지가 함께 정해져야 하기 때문이다.
- **완료 기준**: `redis-cli`로 `SET k v` → `GET k` 동작, `redis-benchmark -t set,get -q` 에러 없이 완주 (이 시점에 `brew install redis`로 클라이언트 도구 설치 필요).
- **함께 정할 사항**: `Handler` panic 시 executor 복구 방식, 에러 응답에 사용자 입력이 섞일 때의 CR/LF 처리.

### 구현 메모

- **파서 (`internal/protocol/parser.go`)**: `Parse(buf)`는 메시지가 덜 도착했으면 `ErrIncomplete`와 `consumed=0`을 반환한다. 호출자는 버퍼를 그대로 두고 더 읽으면 된다(Redis의 query buffer 역할). 잘못된 입력은 `*ProtocolError`로 구분한다.
- **writer (`internal/protocol/writer.go`)**: `Append(dst, v)`는 호출자 버퍼에 이어 붙인다. 여러 응답을 한 버퍼에 모아 한 번에 write하는 pipeline 응답 flush의 기반이다.
- **서버 (`internal/network/server.go`)**: 연결마다 goroutine이 읽기·파싱을 맡고, 명령 실행은 서버당 하나인 executor goroutine이 채널로 받아 직렬화한다. 한 번에 읽은 명령들의 응답은 모아서 write 한 번으로 보낸다. `network`는 `Handler` 인터페이스에만 의존한다(실제 구현은 1-4).
- **종료 순서**: `Close()` → listener/연결 닫기 → 연결 goroutine 종료 대기 → 요청 채널 close → executor 종료 대기.
- **알려진 제한**: inline command(평문 명령) 미지원, 중첩 array 깊이 제한 없음, 큰 bulk를 조각으로 받을 때 매번 처음부터 재파싱(O(n²) 가능).
- **⚠️ query buffer 크기 상한 없음 (미해결)**: `handleConn`의 `query`는 상한 없이 커진다. 클라이언트가 `$536870912\r\n`(512MB)만 보내고 데이터를 천천히 보내면 서버 메모리가 계속 늘어난다. Redis는 `client-query-buffer-limit`(기본 1GB)을 넘으면 연결을 끊는다. 대응안: `len(query)`가 상한을 넘으면 `-ERR` 응답 후 연결 종료(테스트: 상한 초과 입력 시 연결이 닫히는지 확인). 1-4와 함께 또는 Phase 5-1(pipeline) 전에 처리한다.
- 상세 분석: [`docs/summary/20261025-server-close-and-query-buffer-summary.md`](docs/summary/20261025-server-close-and-query-buffer-summary.md)
