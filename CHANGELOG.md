Warning: truncated output (original token count: 81153)
Total output lines: 1671

# Changelog

## [Unreleased]

- Fiabilidad (OAuth-01, parcial): el intercambio social de Kiro (`POST /api/oauth/kiro/social-exchange`) ya no usa `http.Post` sin timeout; ahora respeta la cancelación de la petición del panel y usa el cliente OAuth con timeout de 15 s y reintento directo ante proxies que rechazan CONNECT. Un host de autenticación colgado ya no deja el handler esperando para siempre.

- Seguridad: una API key de cliente ya no puede cambiar el destino de credenciales existentes: `baseUrl`, proxy por conexión (`connectionProxy*`, `strictProxy`), `vercelRelayUrl`, vínculo a proxy pool y cualquier otro campo `*Url`/`*Endpoint` de una conexión, la URL base de un nodo de proveedor ni las URLs/tipo de un proxy pool. Sesión de administrador o token CLI local siguen pudiendo; renombrar, activar, reordenar o asignar modelo con la API key sigue funcionando. Respuesta: HTTP 403 con mensaje en español.

- Seguridad: una API key de cliente (inferencia) ya no puede desactivar el login del panel, fijar o cambiar su contraseña, cambiar el modo SSO/OIDC/SAML, habilitar el acceso al panel por túnel, reescribir `tunnelUrl`/`tailscaleUrl` ni activar Tailscale/Funnel. Esas acciones exigen sesión de administrador o el token CLI local; con `requireLogin=false` el comportamiento no cambia y los ajustes ordinarios siguen aceptando la API key. Respuesta: HTTP 403 con mensaje en español.

- Capimux adds a multimodel comparator with a server-backed model picker, independent ordered results, cancellation and bounded non-streaming responses. Both comparator APIs always require an admin session or local CLI token. Provider dispatch remains opt-in with an exact model allowlist; simulated runs never call providers. Deliberate fork extension beyond decolua/9router.

- Codex OAuth can now delegate login end-to-end to the installed Codex CLI (AnyClaw-style): the CLI owns PKCE, the fixed loopback callback and token exchange inside an isolated temporary `CODEX_HOME`; 9router imports the completed account without exposing credentials in dashboard responses or logs. The existing in-process PKCE flow remains as a fallback when Codex CLI is unavailable. This is an intentional fork improvement beyond current decolua/9router parity.

- Reject empty OpenCode chat completions before committing HTTP 200, so fallback can try the next model in JSON and SSE. Preserve streamed text/tool calls and stop retrying once a response starts. Deliberate fork behavior for coding continuity: upstream decolua/9router preserves reasoning-only completions, which do not satisfy a coding turn's final answer/tool contract.

- Fork order 008 / upstream integration #30: reconcile virtual free routing, NoAuth llama.cpp/local dial guards, media auth and resolution errors with upstream capacity adapters, retry bookkeeping and provider isolation. Keep live provider tests opt-in and ElevenLabs voices tests on a local fixture.

## [v1.9.6] - 2026-10-01

### 🐛 Pre-release review: 5 blocker yang lolos semua gate (#70)

Audit 42 commit `v1.9.5..main` sebelum rilis menemukan lima cacat yang **tidak**
tertangkap `go vet`, `bun test`, maupun `-race`, karena test yang ada memock
hal yang sama persis dengan jalur kodenya. Kelimanya sudah diperbaiki dan
diuji dengan uji regresi yang gagal bila fix-nya dibalik (*mutation-checked*).

- 🔴 **`9router-go status|stop|logs` buta terhadap `DATA_DIR` dari `.env`.**
  `daemonURL()` dan `daemon.Dir()` membaca `os.Getenv`, sedangkan server
  menyelesaikannya lewat viper yang membaca `.env`. Pada deployment yang
  dikonfigurasi lewat `.env` saja — termasuk setiap docker compose — proses CLI
  melihat direktori berbeda dari daemon yang sedang jalan: `status` melaporkan
  *"not running"* padahal ada listener hidup, `stop` menolak untuk halt, dan
  `logs` mengklaim tidak ada log padahal file-nya 50 KB. `ResolveDataDir()`
  kini membaca `.env` (dengan urutan env → `.env` → default platform) dan
  `daemonURL()` memakai `config.LoadConfig()` sehingga port yang diprobe sama
  dengan yang di-bind.
- 🔴 **Nama tool ter-fit bocor ke client di lane Claude `TranslateResp`.**
  `handleClaudeMessagesStream` `return` di baris 19–46, **sebelum**
  `decloaker := NewClaudeStreamDecloaker(req.ToolNameMap)` di baris 78. Request
  sudah di-fit di `fallback.go:362`, jadi `content_block_start` tool_use sampai
  ke client dengan nama 64 karakter dan tidak bisa di-dispatch. Non-stream
  punya cacat serupa (passthrough di `claude_messages.go:191` keluar sebelum
  decloak) — keduanya sekarang decloak sebelum branch mana pun. Decloaker
  di-hoist ke atas `TranslateResp`.
- 🔴 **Lane Responses: request tidak konsisten dengan dirinya sendiri.**
  `FitToolNames` tidak punya walker `input[]` (hanya `tools`/`functions`/
  `messages`/`contents`/`tool_choice`), padahal `RestoreToolNames` tetap
  me-restore `output[]` (`fingerprint.go:282-311`). Akibatnya deklarasi tool
  ter-fit ke 64 karakter sementara history masih memanggil nama aslinya 71
  karakter — upstream menerima request yang bertentangan dengan dirinya, dan
  fix 400-nya sendiri tidak benar-benar terpakai, karena nama panjang tetap
  dibawa lewat `input`. Ditambahkan `visitInput`/`replaceInInput`
  (`tool_fit.go`) yang berbagi satu walker untuk kedua arah, dan
  `passthroughResponses` kini menerapkan `req.ToolNameMap` pada body non-stream
  maupun lewat `sseStreamOpts` pada stream.
- 🔴 **Idempotency key reset-credit kosong — proteksi double-redeem tidak
  pernah ada.** `resetCreditIdempotencyKey` dideklarasikan tapi tidak pernah
  di-assign di mana pun (`newIdempotencyKey()` adalah dead code), sehingga
  tiap request mengirim `idempotencyKey: ""` dan server memint key baru per
  request (`usage_codex_reset.go:176-178`) → dua submit innocuous =
  dua kredit terbuang. Key kini di-mint saat modal dibuka, di-reset saat ditutup,
  dan `confirmResetCredit` menolak mengirim key kosong. Helper-nya dipindah ke
  `lib/codexResetCredit.ts` agar unit-testable sesuai konvensi repo.
- 🔴 **`9router-go stop` di Windows tidak pernah graceful.** `stopGraceMS = 5000`
  dideklarasikan, tapi `requestStop` di `proc_windows.go` selalu
  `ErrStopUnsupported`, jadi `proc.Terminate` langsung `ForceKill` — drain
  `server.Shutdown` 5 detik yang dirancang di `server.go:99-101` **tidak pernah
  jalan** di Windows: setiap `stop`/`restart`/auto-update memotong SSE
  in-flight dan SQL transaction setengah jalan. `Stop()` kini POST ke
  `/api/version/shutdown` lebih dulu **dengan CLI token** (endpoint-nya
  always-protected; tanpa token selalu 401 dan jatuh ke force-kill), dengan
  `proc.Terminate` sebagai fallback untuk daemon yang tidak menjawab. Terverifikasi
  live: log `Server stopped gracefully` pada 4 siklus restart beruntun.
- 🔴 **Data race `usagetracker`** (ketemu saat rerun `-race`, pre-existing dan
  tidak terkait 5 fix di atas): `scheduleBroadcastLocked` menyalin daftar
  subscriber lalu melepas lock **sebelum** send, sedangkan `unsubscribe` menutup
  channel di bawah write lock → *send on closed channel*. Map-nya terbaca aman,
  channel-nya tidak. Kirim dipindah ke bawah `RLock`. Catatan: `8520e4e`
  mengklaim "fix data race in usagetracker" tetapi hanya menambal ring seeding.
- **Batas 64 karakter sekarang benar-benar diuji.** Fixture lama adalah 63 dan
  65 karakter, jadi tidak ada yang mem-*pin* tepat di batas — dan batas itulah
  yang jadi inti fitur ini. Ditambah `TestFitToolNames_ExactBoundary`.
- **Verifikasi:** `go vet ./...` + `-tags=integration` 0 warning;
  `go test -race -count=1 ./...` exit 0; `go test -tags=integration` ok;
  `bun test` 115/115; `tsc -b`/`oxlint`/`bun run build` bersih;
  `make build` + `make cross` (5 platform) sukses. Live: lifecycle daemon
  start/status/restart/stop tanpa `DATA_DIR` ter-export, tool 71 karakter
  kembali ke client sebagai 71 karakter, 7 route dashboard tanpa console
  error.
- **Ditunda (bukan blocker, tidak ikut PR ini):** self-update bisa memasang
  binary tanpa verifikasi saat manifest tidak punya `sha256` (`updater.go:411`;
  `release.yml` sudah menerbitkan `SHA256SUMS.txt` tapi tidak ada kode Go yang
  membacanya); `parseSemver` membuang suffix prerelease sehingga RC bisa
  terbaca lebih baru dari final dan di-auto-apply; `RunningPID` hanya percaya
  PID telanjang sehingga PID daur-ulang bisa membuat `stop` membunuh proses
  lain; `gateway.log` tidak pernah dirotasi dan dibaca utuh tiap 150 ms;
  `signalSelfShutdown` kini dead code di kedua varian build; `tools[].toolSpec.name`
  (Bedrock Converse) direkam tapi tidak ditulis.

### 🐛 MCP tools dengan nama fungsi > 64 karakter mental dengan HTTP 400 (#68)

- **Masalah:** Spesifikasi fungsi OpenAI / OpenAI-compatible membatasi panjang `function.name` maksimal 64 karakter (`^[a-zA-Z0-9_-]{1,64}$`). Coding agent dengan integrasi server MCP sering kali menggunakan nama namespaced (misal `mcp__server_name__action_detail_something`) yang melebihi 64 karakter, menyebabkan upstream provider (OpenAI, Console, Responses API, dll.) menolak request dengan status 400 Bad Request (`name must be at most 64 characters, got XX`).
- **Perbaikan:**
  - Ditambahkan `translator.FitToolNames` yang secara deterministik memangkas nama fungsi yang melebihi 64 karakter menjadi maksimal 64 karakter dengan sufiks unik `_1`, `_2`, dst (memperhitungkan panjang sufiks sehingga total panjang tidak pernah melebihi 64 karakter dan tidak bentrok dengan tools lain).
  - Mengganti seluruh referensi nama fungsi di deklarasi `tools`, `functions`, conversation history (`messages` assistant `tool_calls`, `function_call`, `role: "tool"`/`role: "function"`, Claude `tool_use`), dan `tool_choice`.
  - Mengintegrasikan pemulihan nama via `NewToolNameRestoringWriter` dan `RestoreToolNamesInPayload`, sehingga respons dari upstream (baik streaming SSE maupun non-streaming JSON, OpenAI/Claude/Responses/Gemini) dikembalikan ke nama asli yang panjang sebelum diteruskan ke client.
  - Sesi multi-turn percakapan tetap sinkron karena pemotongan nama bersifat deterministik.
- **Verifikasi:** Unit test `TestFitToolNames_*`, `TestRestoreToolNames_*` di `internal/translator/tool_fit_test.go` dan end-to-end integration test `TestE2E_FitToolNames_*` (non-streaming, SSE streaming, multi-turn) di `internal/handlers/chat/tool_fit_e2e_test.go` lolos dengan `go test -race` dan `go vet`.

### 🐛 Fix dashboard feedback issues: login lockout, remote password rotation, proxy dropdown, combo model drag-and-drop, and zip database backup (#50)

- **Login limiter IP bucketing**: `LoginClientIP` in `internal/auth/session.go` no longer falls back to `"unknown"` when no proxy headers are present. Direct TCP peer IP from `r.RemoteAddr` is used so distinct clients have their own failure buckets and one misconfigured client does not lock out all other users.
- **Remote / Docker initial password rotation**: `POST /api/auth/login` now accepts `{ password, newPassword }`. Remote and Docker fresh installs requiring default password rotation can set their new password directly and receive a valid session cookie without encountering 401 Unauthorized from protected settings endpoints.
- **Initial password change check**: `changeDashboardPassword` in `internal/handlers/dashboard/settings.go` now validates against `INITIAL_PASSWORD` when no password hash is stored.
- **Provider proxy dropdown & Antigravity Free glitch**:
  - In `ProviderDetailView.svelte`, the connection row Proxy button is now always rendered even if no proxy pools exist yet, displaying a clear empty state with a shortcut to create one.
  - The proxy dropdown now uses `position: fixed` relative to the trigger button to prevent clipping inside the scroll container (`overflow-y-auto`).
  - Wrapped `loadData()` inside `untrack` so that background polling of `connections` does not continuously re-trigger `loadData()`, eliminating the re-render flash / glitch on free providers like OpenCode Free and preventing proxy selection from resetting.
- **Combo model drag-and-drop & picker performance**:
  - Implemented HTML5 drag-and-drop reordering (`draggable`, `ondragstart`, `ondragover`, `ondrop`, `ondragend`) on model rows in `CreateComboModal.svelte` with active drag visual indicators.
  - Preserved stable alphabetical ordering in `pickerData.ts` to eliminate layout shift, frame drops, and freezing when clicking model pills in `ModelPickerModal.svelte`.
  - Added module-level caching for model picker metadata (`pickerExtras`) so opening the picker does not flash empty states or block UI interactions.
- **Database backup download & ZIP archive support**:
  - Deferred `URL.revokeObjectURL` in `ProfileSettingsView.svelte` to prevent modern Chromium/Firefox download managers from cancelling or blocking in-flight blob downloads.
  - Added support for `?format=zip` in `GET /api/settings/database` to export backups as standard compressed `.zip` archives with `Content-Disposition: attachment`.
  - Added support for importing `.zip` archives in `POST /api/settings/database`, automatically extracting and restoring the JSON payload.
  - Updated `ProfileSettingsView.svelte` to download `.zip` by default and accept `.zip` as well as `.json` imports.

### ✅ Binary bisa jalan di background — `9router-go start` / `stop` / `restart` / `status` / `logs`

- **Opsi baru:** `--background` (alias `-d`) dan sub-command `start` menjalankan gateway sebagai proses terpisah yang tidak menempel ke terminal, lalu langsung kembali. Sub-command baru: `stop`, `restart`, `status`, `logs -n <baris>`. Perilaku lama (`9router-go` tanpa flag) **tidak berubah** — tetap jalan di foreground dan berhenti saat `^C`.
- **PID file & log:** proses yang terpisah mencatat dirinya di `DATA_DIR/run/gateway.pid` dan menulis stdout/stderr ke `DATA_DIR/run/gateway.log`. File PID basi dibersihkan saat command berikutnya jalan, sehingga daemon yang dibunuh paksa dari Task Manager tidak meninggalkan jejak yang menyesatkan.
- **`Start` menolak saat port sudah dipakai** — pre-flight TCP connect, bukan sekadar mengandalkan PID file. Diuji dengan server Python yang memegang port: `start` gagal dengan pesan yang menyebut port dan sumbernya, bukan melombakan dua proses untuk satu bind.
- **Dua bug Windows nyata yang ketemu dan diperbaiki di jalur yang sama.** (1) `HandleShutdown` (tombol Shutdown di dashboard) self-signal `SIGTERM`, dan `os.Process.Signal(syscall.SIGTERM)` di Windows mengembalikan `not supported by windows` — tombolnya tidak pernah menghentikan server. (2) `RestartSelf()` melakukan `os.Exit(0)` sebelum listener ditutup, lalu spawn spawn replacement, sehingga proses baru bisa rebut port sebelum yang lama melepaskannya. Keduanya kini lewat `shutdown.RequestStop()` / `shutdown.RunAfterStop()`: main menunggu tiga sumber (`SIGINT`, `SIGTERM`, stop request), menjalankan `fxApp.Stop`, baru menjalankan hook spawn. Terbukti: `POST /api/version/shutdown` dengan session cookie → proses mati, port `20197` benar-benar lepas.
- **Detail restart yang sempat bikin bug:** `Restart` harus **stop dulu baru pre-flight**. Kalau pre-flight dijalankan lebih dulu, ia menemukan daemon lama yang masih listen dan reported "already running"; kalau dijalankan sesudah `TerminateProcess`, socket-nya baru diedit sangat sedikit. Urutan stop → tunggu port bebas (maks 10 dtk) → start sudah teruji: pid `21700` → restart → pid `17628`, `/health` tetap `{"status":"ok"}`.
- **Baru `internal/proc`:** primitif proses portabel ( Alive / Terminate / Detached / SelfExecutable ) dipakai bersama oleh daemon **dan** headroom; `internal/headroom/sig_unix.go` + `sig_windows.go` yang menduplikasi logika yang sama dihapus.
- **Yang berubah di API shutdown:** `shutdown.RequestStop()` menutup channel baru `StopRequested()` sekaligus memicu `Cancel()` (SSE berhenti). `Cancel()` saja **tidak** menutup `StopRequested()` — `^C` bukan permintaan keluar dari operator, dan main harus terus menunggu sinyal asli. Dipin di `internal/shutdown/shutdown_test.go`.
- **Verifikasi:** `go vet ./...` bersih, `go build ./...` bersih, `go test ./internal/...` semua paket hijau kecuali `TestApplyConnectionStrategy_KeepsRotatingPastFirstCycle` yang **sudah gagal sebelum perubahan ini** (dibuktikan di worktree bersih pada `HEAD` `9323d21`, bukan efek samping). Smoke penuh di Windows: start → `/health` `{"status":"ok"}` → `/login` 200 → status → restart (pid baru) → health → stop → pid file hilang → port tidak lagi `LISTENING`; `-d` identik dengan `start`; start kedua refused "already running"; foreground tidak menulis pid file sama sekali.

> ⚠️ **Perbedaan perilaku antar-platform yang disengaja:** di Windows tidak ada SIGTERM, jadi `stop` memakai `TerminateProcess` (proses langsung mati, drain SSE dilewati). Di POSIX, stop mengirim SIGTERM sehingga hook Fx berjalan dan `runServer` keluar bersih. Yang butuh drain (update via dashboard, restart) sebaiknya lewat `restart` atau tombol Shutdown, bukan `stop` di Windows.

### 🐛 Makefile hanya jalan di POSIX shell — di Windows setiap target build/run rusak

- 🔴 **Semua gejala berasal dari sintaks POSIX di dalam recipe.** `VERSION ?= $(shell cat VERSION 2>/dev/null …)` mengembalikan string kosong di shell non-POSIX (`/dev/null` tidak ada), jadi `-X …CurrentVersion=` meng-embed versi **kosong** ke binary. `PORT=20130 ./9router-go` membuat cmd.exe mencoba menjalankan program bernama `PORT`. `run`/`version`/`update`/`mitm-*` memanggil `./$(BINARY_NAME)`, dan cmd.exe menjawab `'.' is not recognized as an internal or external command` karena `.` tidak ada di PATH (PATHEXT hanya menyertakan `.EXE`). `LDFLAGS` diapit tanda kutip tunggal, dan cmd.exe memperlakukan `'` sebagai karakter literal sehingga linker menerima nama simbol yang sudah ter-quote. `DATA_DIR ?= $(HOME)/.9router` expandable jadi kosong di native Windows → path literal `/.9router` → `C:/Program Files/Git/.9router`, sehingga **setiap** `make run` menulis DB sementara yang baru dan dashboard membalas 401 terus-menerus.
- **Perbaikan:** `VERSION` dibaca lewat `$(file <VERSION))` (Make native, tanpa shell) dengan fallback berurutan ke `version.json` lalu `git describe` lalu `1.0.0`; binary dipanggil sebagai `$(BINARY)` tanpa `./` (ditemukan lewat PATHEXT di cmd.exe, tetap jalan di sh/POSIX); `LDFLAGS` memakai tanda kutip ganda; `DATA_DIR ?=` dibiarkan kosong agar binary menerapkan default per-platform-nya sendiri (`%APPDATA%\9router` / `~/.9router`).
- **`PORT`/`DATA_DIR`/`RTK`/`CAVEMAN`/`PONYTAIL` hanya diekspor kalau asalnya bukan `file`.** `make run` polled tidak boleh menimpa config: nilai `?=` punya origin `file`, sedangkan yang datang dari environment atau command line punya origin `environment`/`command line`. Yang di-set operator tetap diteruskan; yang tidak di-set dilewati agar binary membaca `.env` (viper) sendiri seperti biasa. Ini menggantikan prefix `VAR=value` yang tidak bisa di-parse cmd.exe.
- **`web-build` dan `cross` tidak lagi butuh POSIX.** Guard `[ ! -f web/dist/index.html ]` mati di cmd.exe (`! was unexpected at this time.`), jadi seluruh pemeriksaan-keberadaan dan build didelegasikan ke `bun -e` — Bun sudah jadi prasyarat setiap jalur yang lewat target ini. `cross` tidak lagi menulis `GOOS=… GOARCH=…` di depan `go build`; per-target OS/ARCH masuk lewat direktif `export` yang identik di sh maupun cmd, dan `export`-nya di-scope ke subgoal `cross-one` supaya `GOARCH` kosong tidak pernah bocor ke `build` biasa.
- **Verifikasi:** `make version` menghasilkan `9router-go version 1.9.5 (windows/amd64)` baik saat make dipanggil dari sh maupun dari `cmd.exe /c`; `make cross-one CROSS_OS=linux CROSS_ARCH=amd64` dari Windows menghasilkan binary ELF sungguhan (magic `\x7fELF`), jadi plumbing `export GOOS/GOARCH` terbukti bukan sekadar lolos parse. Recipe `cross` (loop `for`), `clean` (`rm -f`), `help` (`grep|sed`), dan `bench` masih POSIX-saja — di mesin tanpa `sh.exe` di PATH, recipe itulah yang pertama gagal.
- 🔴 **Follow-up: `BINARY_NAME` tidak punya ekstensi, jadi di Windows build baru tidak pernah dijalankan.** `go build -o 9router-go` di native Windows menulis file bernama `9router-go` (PE tanpa ekstensi), sementara `make run` memanggil `9router-go`, yang cmd.exe resolve lewat `PATHEXT` ke **`9router-go.exe` milik build sebelumnya**. Akibatnya `make run` diam-diam menjalankan binary lama: aset `go:embed` tetap bundle SPA yang sudah usang, dan perbaikan frontend tampak "tidak masuk" padahal sudah di-build. Diperbaiki dengan `BINARY_SUFFIX := $(if $(findstring Windows_NT,$(OS)),.exe,)` sehingga target build menulis persis file yang dieksekusi. `BINARY` juga dipisah per-OS: bare name di Windows (PATHEXT), `./$(BINARY_NAME)` di macOS/Linux (`.` tidak ada di PATH).
- **`web-build` bisa dipaksa rebuild.** Kondisi `existsSync('web/dist/index.html')` membuat SPA hanya dibangun sekali; sekarang `FORCE=1 make web-build` (atau `FORCE=1 make build`) membangun ulang `web/dist`. Docker (`Dockerfile` stage `web-builder`) dan GitHub Actions (`ci.yml`, `release.yml`) **tidak** terpengaruh: keduanya sudah memanggil `bun run build` secara eksplisit di build context bersih, jadi tidak pernah melewati cache ini.


### 🐛 Dua error console dashboard: tombol "Add Model" tidak pernah membuka picker, dan event install dipakai ulang

- **Gejalanya satu, root cause-nya lain, dan keduanya sudah terukur di browser.** Membuka modal Create/Edit Combo lalu menekan **Add Model** melempar `effect_update_depth_exceeded` — picker tidak pernah tampil, tidak ada input pencarian di DOM. Efek reset di `CombosView.svelte` menulis `modalNameResetKey`, nilai yang dibaca blok `{#key}` di bawahnya; blok itu membuat ulang `CreateComboModal`, dan itu mengantrekan ulang efeknya. Svelte membuang seluruh flush setelah **1001** putaran (terhitung di dev build: `window.__resetRuns = 1001` tepat saat modal Create terbuka, sebelum "Add Model" ditekan). Efeknya kini dijaga `createSessionOpen`, jadi reset terjadi sekali per sesi — diverifikasi di build production: picker terbuka, search 77 → 36 pill → kembali 77, klik pill toggle, **0 error console**.
- **Bukan regression dari #61.** Bug ini direproduksi juga dengan kode pra-#61 (`git show a8b1556^`), dan dibisect: menghapus `modalNameResetKey += 1` saja (sambil membiarkan `modalModels = []`) membuat efek berhenti di **1** putaran dan error hilang. Jadi penyebabnya terukur, bukan dugaan.
- **Event `beforeinstallprompt` sekarang benar-benar sekali pakai.** `promptInstall()` lama menyimpan event setelah dialog di-*dismiss*, jadi klik berikutnya memanggil `prompt()` pada event yang sudah dipakai dan Chrome menolak dengan "Ignored bad install" di console. Sekarang event di-*spend* pada panggilan pertama (accepted maupun dismissed) dan CTA-nya mati sampai Chrome mengirim event baru; kalau app sudah berjalan standalone, event tidak lagi di-*preventDefault* karena tidak ada tombol custom yang memakainya. Dites di `web/src/lib/pwa.test.ts` (4 kasus) dan **mutation-checked**: menghapus guard standalone atau mengembalikan event ke keadaan "belum dipakai" membuat 3 dari 4 test gagal pada asersi yang tepat.
- **`s.includes is not a function` — akhirnya terReproduksi dan diperbaiki.** Akar masalahnya ketemu: `internal/db/accounts.go:LockConnectionRateLimit` menulis `lastError` ke `providerConnections.data` sebagai **JSON object** (`{"status":429,"message":"…","timestamp":"…"}`, dipakai juga oleh `RecordConnectionOAuthFailure`), sedangkan halaman provider membaca `conn.lastError` lalu memanggil `.includes('429')`/`.toLowerCase().includes('quota')` di `getCooldownInfo`. Begitu sebuah koneksi kena rate limit, `s` adalah object dan `.includes` melempar `TypeError` — seluruh render halaman mati. Diperbaiki di tiga lapis: (1) **backend** `sanitizeProviderConnection` menormalkan `lastError` apa pun (object/string/lainnya) menjadi string sebelum keluar dari server; (2) **API client** `normalizeConnection`/`normalizeLastError` melakukan hal yang sama pada semua jalur ambil koneksi (`getConnections`, `getProvidersClient`, `getProvidersClientPage`); (3) **komponen** memakai helper yang sama, jadi halaman provider tidak pernah memanggil method string pada nilai non-string. Diuji dengan `TestSanitizeProviderConnection_LastErrorString` (backend) dan dua kasus `client.test.ts` yang menutup ketiga jalur fetch.
- **`/api/models/caps` tidak lagi 404 untuk compatible node.** Halaman `openai-compatible-*` / `anthropic-compatible-*` dan custom provider node selalu meminta caps; backend menjawab `404 unknown provider or no static models` karena node itu memang tidak punya katalog statis, jadi tiap pembukaan halaman menambah request merah di console. Handler kini menjawab `200 {"provider":…,"caps":{}}` untuk id yang ditandai sebagai node, dan halaman provider tidak lagi memanggil endpoint itu sama sekali bila provider-nya bukan katalog statis. Alias dan id katalog tetap ikut ter-cover: seluruh provider Go yang punya model statis (92 id kanonis) ada di `PROVIDER_CATALOG`, jadi ikon kapabilitas dan picker thinking tidak hilang.
- **Combo `Auto Free Tier` tidak lagi terkunci permanen — bisa diedit, di-rename, dan dihapus.** Alasannya: feature ini berasal dari PR #58 dan **tidak ada di upstream**. `src/app/api/combos/` upstream hanya punya `[id]` dan `presets`, tidak ada `auto-free`; route `DELETE /api/combos/[id]` upstream juga **tidak** punya guard apa pun. Sementara itu di port ini combo-nya di-lock di tiga tempat: `validateLockedReorder` menolak rename/kind/ubah model set (`combos.go`), `HandleDeleteCombo` menolak `DELETE` dengan 403, dan `ComboCard` menonaktifkan checkbox/Edit/Delete. Akibatnya begitu tombol **Auto Free Tier** diklik sekali, baris `auto-free-tier` ada selamanya dan tidak bisa dilepas dari dashboard.
- **Yang dihapus:** `validateLockedReorder` + `sameModelSet` + guard di `HandleUpdateCombo`/`HandleDeleteCombo` (backend), `isAutoFreeCombo` beserta filter `deletableCombos` dan guard `toggleSelect` (frontend), badge "reorder only" beserta tombol Edit/Delete yang mati, dan jalur `onReorder` yang sekarang mati — panah Move up/down di kartu dihapus karena Edit modal sudah menyediakan reorder yang sama untuk semua combo. `isAutoFreeCombo` digantikan `isAutoGenerated` yang hanya menandai kind `auto-*` untuk badge "Auto-generated", sama seperti `auto-family-*` yang sejak awal memang editable.
- **Yang dipertahankan:** `POST /api/combos/auto-free` tetap upsert pada id stabil `auto-free-tier`, jadi menghapus combo lalu klik lagi akan membuatnya kembali dengan isi registry terkini. Tipe `kind = "auto-free"` tetap ditulis agar kartu bisa menandainya sebagai machine-generated.
- **Verifikasi:** `TestAutoFreeComboCreateRebuildAndEdit` (menggantikan `TestAutoFreeComboCreateAndLock`) menjalankan route sungguhan lewat `HandleAutoFreeCombo` → rename 200 + nama tersimpan → edit model set 200 + 2 member tersimpan → update strategy-only tidak menimpa model set → `DELETE` 200 + baris hilang → rebuild 200 + nama & model set kembali ke isi registry. Live di `localhost:20130`: `DELETE` menjawab 200 (sebelumnya 403), rename 200, edit model set 200, dan rebuild mengembalikan combo ke keadaan semula. Browser di `localhost:5173/dashboard/combos`: kartu Auto Free Tier kini ber-badge "Auto-generated", checkbox/Edit/Delete aktif, modal Edit dan modal konfirmasi Delete keduanya terbuka, 0 error console. `go vet`, `go test ./...` (32 paket), integration suite, `bun test` 113/113, build bersih.
- **Model embedding tidak lagi masuk rantai fallback `Auto Free Tier`.** Penanda free-tier hanyalah konvensi penamaan — `IsFreeTierModel` mencocokkan sufiks `:free` / `/free` / `-free`, dan sufiks itu juga menempel pada model embedding, image, tts, dan stt. `openrouter` punya tepat satu model free-suffix, yaitu `nvidia/llama-nemotron-embed-vl-1b-v2:free`, yang registry sudah tandai `"embedding"` di `ProviderModelKinds` — tapi `freeTierComboModels` tidak pernah memanggil `GetProviderModelKind`, sehingga model yang tidak bisa menjawab satu turn chat pun tetap masuk daftar. Sekarang satu baris gate `kind != "" && kind != "llm"` membuangnya, persis seperti yang sudah dilakukan `usableModelFamilies` untuk combo auto-family. Census registry: 40 model free-suffix, 35 di antaranya chat-capable, tersebar di 12 kunci provider — jadi fitur ini bukan formalitas, combo untuk/setup yang punya 6 provider gratis berisi belasan model chat.
- **Tombol "Auto Group by Model" dan "Auto Free Tier" dihapus dari header halaman Combo.** Keduanya adalah generator massal yang sekali klik menulis puluhan baris `combos` — 40 model free-suffix di registry, dikelompokkan per keluarga, sudah menghasilkan 80 combo `Auto: …` di satu dashboard. `CombosHeader` kehilangan keempat prop auto-builder, `CombosView` kehilangan `handleBuildAutoFamily` beserta `isBuildingAutoFamily`, dan `api.buildAutoFamilyCombos` ikut dibuang karena tidak lagi dipanggil siapa pun.
- **Halaman Combo disamakan dengan upstream v0.5.91 (`localhost:20128/dashboard/combos`).** Yang dibandingkan langsung di browser, bukan dari reading kode. Upstream memakai **satu** tombol di header — `Create Combo` — dan memindahkan seluruh aksi massal ke *selection bar* di bawah daftar: checkbox `Select all (N)` di kiri yang berubah menjadi `N selected` begitu ada yang dipilih, lalu di kanan `Set strategy…`, `Delete (N)`, dan `Clear` yang **hanya muncul saat ada selection**. Port ini sebelumnya menyimpan `Delete Selected` dan `Delete All (N)` sebagai tombol toolbar permanen, tidak punya selection bar sama sekali, dan punya tombol Rebuild per kartu yang tidak ada di upstream.
- **Apa yang berubah:** `CombosHeader` sekarang hanya menerima `onCreateClick`. `CombosView` mendapat `selectedCombos`/`allSelected`/`toggleSelectAll`, `handleApplyBulkStrategy` (menulis `comboStrategies` sebagai satu patch lalu satu `updateCombo` per baris, karena key-nya adalah *nama* combo sehingga patch terpisah akan menimpa dirinya sendiri), dan selection bar yang men…66153 tokens truncated…unt` from Gemini stream and non-stream responses into `OpenAIUsage.CachedTokens`, providing accurate cache hit reporting and cost calculation. (`internal/translator/gemini.go`)
- **Gemini Vision FileData & Audio Modalities** — added support for remote HTTP/HTTPS image URLs (`fileData: { fileUri, mimeType: "image/*" }`), base64 input audio (`input_audio`, `audio_url`), and uploaded documents in Gemini native translator, matching Next.js full multimodal capabilities. (`internal/translator/gemini.go`)
- **Realtime SSE Usage Stream & Topology Animation** — added in-memory in-flight request tracker (`internal/usagetracker`), real-time SSE broadcasting (`GET /api/usage/stream` and `GET /usage/stream`), and recent requests ring buffer matching the Next.js dashboard shape, enabling instant glowing pulse node & marching-ants edge animations on the Usage Topology graph when requests are handled by `9router-go`. (`internal/usagetracker/tracker.go`, `internal/handlers/usage_stream.go`, `internal/handlers/chat/fallback.go`, `internal/handlers/chat/usage.go`)
- **Antigravity Anti-Competitive Prompt Stripping & 429 Prevention** — automatically strips competitor identity phrases (e.g. `"You are a Claude agent, built on Anthropic's Claude Agent SDK."` from Zed IDE and Claude agents) from `system_instruction` and message contents, preventing Antigravity from returning synthetic `429 Quota Exhausted` errors. (`internal/translator/antigravity.go`)
- **Edge Relay URL Rewriting & Header Forwarding** — automatically rewrites `BaseURL` to the relay deployment and injects `x-relay-target` and `x-relay-path` headers when a connection uses a Vercel, Cloudflare Worker, or Deno Edge Relay Proxy Pool. (`internal/handlers/chat/connections.go`)
- **No-Auth Provider Proxy Pool Strategy** — automatically respects `settings.providerStrategies` for no-auth providers (e.g. `mimo-free`, `opencode`), attaching configured proxy pools or rotation strategies to virtual connections. (`internal/handlers/chat/connections.go`, `internal/db/settings.go`)
- **Snake_case Model Limits on `/v1/models` & `/v1/models/info`** — exposes `context_length`, `max_completion_tokens`, `max_input_tokens`, and `max_output_tokens` so clients like Cline, Roo Code, and LibreChat resolve proper context ceilings. (`internal/handlers/chat/chat.go`, `internal/providers/capabilities.go`)
- **CodeBuddy OAuth Configuration** — registered `codebuddy-cn` and `codebuddy-intl` OAuth token refresh configurations. (`internal/providers/oauth.go`)
- **OpenCode Official Client Fingerprint Headers** — injects official headers (`User-Agent: opencode`, `x-opencode-client: desktop`, `x-opencode-session: ses_...`, `x-opencode-request: msg_...`, `x-opencode-project: global`) on free-tier OpenCode requests to prevent rate limiting from unidentified client traffic. (`internal/proxy/opencode.go`, `internal/proxy/executor/providers.go`)
- **Kimchi Dual Authentication** — supports direct API keys (`Authorization: Bearer <key>`) in addition to OAuth tokens with seamless credential resolution. (`internal/handlers/chat/connections.go`, `internal/handlers/chat/kimchi_handler_test.go`)
- **Startup Banner & Version Display** — dynamically displays current version in CLI startup banner (`🚀 9Router Go Proxy (v1.8.3) on :20130`) and server ready logs. (`cmd/9router-go/main.go`)
- **New Provider Registries & Aliases** — added Alibaba Token Plan Singapore (`alitp-intl` / `ali-tp` / `alitp`) and Fish Audio Text-to-Speech (`fish-audio` / `fish`). (`internal/providers/providers.go`, `internal/providers/aliases.go`)

### 🐛 Bug Fixes

- **Invalid Tool Parameters & Decoy Schemas** — provided valid non-empty `properties.reason` schema for all 21 Antigravity decoy tools and mapped `tool_call_id` to exact function names in OpenAI-to-Gemini conversation history, eliminating protobuf validation errors when using Claude Code or other tool-calling clients. (`internal/translator/antigravity.go`, `internal/translator/gemini.go`)
- **Antigravity Upstream Model Resolution** — prevented invalid model aliases like `gemini-3.7-flash-high` from reaching Google Cloud Code without being translated to their backend model IDs. (`internal/translator/antigravity.go`)

### 📚 Documentation

- Comprehensive refresh of `README.md`, `COMPARISON.md`, `DATABASE.md`, `ARCHITECTURE.md`, `TECHNICAL_DEBT.md` (0 open items), and newly added `ROADMAP.md`.

## [v1.8.2] — 2026-08-14

### ✨ Features

- **Antigravity Tool Cloaking & Anti-Ban Decoy System** — automatically cloaks client tool declarations with `_ide` suffixes (e.g. `Bash_ide`), injects 21 official Antigravity IDE decoy tools (`run_command`, `replace_file_content`, `grep_search`, `list_dir`, etc.), synchronizes conversation history functionCall/functionResponse names, and seamlessly uncloaks tool names on response SSE stream and non-stream outputs. (`internal/translator/antigravity.go`, `internal/translator/gemini.go`)
- **Antigravity Native Image Generation** — added image model detection (`imagen`, `*image*`), aspect ratio suffix parsing (`16x9`, `4:3`, `1:1`, custom resolutions via GCD reduction), `requestType: "image_gen"` envelope wrapping with forced non-streaming `/v1internal:generateContent`, and OpenAI-compatible base64 image response formatting. (`internal/translator/antigravity.go`, `internal/proxy/gemini.go`)
- **Edge Relay & Transport Engine** — added transport support for Vercel, Cloudflare Worker, and Deno edge relays using `x-relay-target` and `x-relay-path` headers, wildcard `noProxy` domain filtering, and legacy connection-level proxy configuration fallback (`connectionProxyUrl`). (`internal/proxy/transport.go`, `internal/handlers/chat/connections.go`)

### 🐛 Bug Fixes

- **Proxy Pool DB Parsing Bug** — fixed `GetProxyPool` (`internal/db/proxyPools.go`) failing to parse single string `proxyUrl` created by Next.js UI / `InsertProxyPool`, which previously caused proxy pools to be silently ignored and requests to fall back to direct connections. Added parsing for `type`, `noProxy`, and `strictProxy` metadata.

## [v1.8.1] — 2026-08-12

### ✨ Features

- **Combo strategy sync with Next.js reference** — per-combo rotation state, correct auto-switch ordering, and a capabilities provider (`internal/providers/capabilities.go`) replacing the hardcoded vision/pdf maps with tiered capability detection.
- **Flatten nested combos** — `combo-wombo → free-tier` now expands to its four leaf models, so round-robin actually rotates across them instead of always landing on the first leaf (this was hammering one account and producing the `429 all connections for this provider are rate-limited` error).
- **Turn-aware rotation** — `applyComboStrategy` advances the rotation index only on a new turn; mid-turn tool-use requests reuse the model serving the turn, so the provider never switches mid-turn (which broke Gemini thinking models that require a `thought_signature` on current-turn function calls).
- **Bounded retry-once on total combo 429** — when every combo model fails with a Retry-After ≤ 8s, the pass waits once and retries before surfacing a hard 429 (`comboRetryAfter`).
- **Backfill default `thought_signature`** — every `functionCall` part now carries a `thoughtSignature` (the real one via `__ts__` transport when present, else the Next.js `DEFAULT_THINKING_AG_SIGNATURE`), closing the last 400-`thought_signature` gaps on mixed combos.
- **Gemini tool-schema keyword parity** — strip the remaining unsupported JSON-Schema keywords (`multipleOf`, `uniqueItems`, `contains`, `unevaluated*`, `contentSchema`) and fill bare `{}` schemas with the object placeholder, matching Next.js `cleanJSONSchemaForAntigravity` (fixes `Invalid tool parameters` 400 from antigravity).
- **Sanitize tools on the OpenAI-compat Gemini path** — the `gemini` provider is now marked `gemini-openai` and its `/v1beta/openai` bodies are run through `SanitizeOpenAITools`, so the strict schema validation applies on both Gemini routes.

### 🐛 Bug Fixes

- **Emit camelCase `thoughtSignature`** — the Gemini-native `generateContent` endpoint only recognizes the camelCase part field; the snake_case regression caused the `400 Function call is missing a thought_signature` error. Both read and write directions now handle camelCase.
- **Use the daily antigravity endpoint** — migrate `cloudcode-pa.googleapis.com` → `daily-cloudcode-pa.googleapis.com` for the `antigravity` provider (`providers.go`, `antigravity_project.go`, MITM domain list) to avoid strict rate limits.
- **Combo connection retry-loop parity** — connection retry loop now matches the single-model path.

### 🧹 Chores / Docs

- Remove the stray `patch_combo.go` throwaway script.
- Add design specs and implementation plans for the combo sync / thought_signature / backfill / schema-parity work.

## [v1.8.0] — 2026-08-11

### 🐛 Bug Fixes

- **Combo/router fallback bypasses model-lock backoff on retryable errors → antigravity rate-limit loop** — `handleComboFallback` / `handleMessagesComboFallback` (`internal/handlers/chat/combo.go`) called `tryForwardWithConnection` directly, so `LockConnectionModel` was never invoked on retryable errors (429/500s) — unlike the single-model path `handleAccountFallback`. The exponential 429 backoff was dead in the router path: every request re-tried all combo models back-to-back on the same connection/account, got 429, returned 429, and the client's ~35s retry repeated the loop forever. Fix:
  - New `comboLockRetryable` helper runs on every `RetryableStatusCodes` error in both combo loops — classifies via `ClassifyError`, calls `LockConnectionModel(connID, model, cooldownSec, newBackoffLevel)` so the exponential backoff persists across requests, and appends the conn to a request-local `excludeIDs` passed into `getBestConnection` so remaining combo models don't re-select the same connection (same account = same quota bucket).
  - A locked-connection skip covers pinned connections whose direct-fetch branch bypasses `getBestConnection`'s lock check.
  - `context.Background()` → `ctx` in `handleComboFallback` so client cancels propagate; the 502/503/504 transient-wait sleep is preserved.
  - Test: `TestHandleMessagesComboFallback_429LocksAndExcludesConnection` asserts a 429 locks the connection AND keeps the second combo model from re-hitting it (exactly 1 upstream hit).

## [v1.7.2] — 2026-08-08

### 🐛 Bug Fixes

- **Antigravity 429/404 failure-loop fix** — An unprovisioned Antigravity account
  (`onboardUser` returns `200` with an empty `cloudaicompanionProject`) left the
  connection without a `projectID`. The router then force-refreshed the OAuth
  token on every request (never an auth problem, so it never helped), fell through
  to a guaranteed-404 OpenAI-compatible lane on `cloudcode-pa.googleapis.com`,
  and repeated client retries rammed Google's rate limit (`429`). (`internal/handlers/chat/gemini_handler.go`, `internal/handlers/chat/antigravity_project.go`)
  - `fetchAntigravityProjectID` now reports the outcome (`projectID`, `authFailed`,
    `noProject`). Token refresh runs **only** on a genuine `401/403` — never on a
    missing/empty project.
  - When antigravity has no project ID, it no longer burns a request on the dead
    OpenAI lane; it returns an error and the fallback chain moves straight to the
    next provider.
  - **Negative cache (10 min, per connection):** once Google confirms "no project",
    later requests skip the `loadCodeAssist`/`onboardUser` RPCs entirely — this is
    what stops the repeated `429` hammering.
  - Onboarding guidance is logged once per connection per window
    ("onboard the account via Antigravity IDE/CLI, then re-login"); repeated
    failures log at `Debug` instead of spamming `Warn`. (`internal/handlers/chat/fallback.go`)

### 🧪 Tests

- `antigravity_project_test.go` — pins the probe classification (project found /
  token rejected `401`+`403` / project definitively missing / transient `429`+`503`)
  and the negative-cache expiry semantics. (`internal/handlers/chat/antigravity_project_test.go`)

## [v1.7.1] — 2026-08-08

### 🐛 Bug Fixes

- **Cached-token parity across every provider** — Prompt-cache accounting no longer works only for antigravity. Gemini `usageMetadata.cachedContentToken` now flows through both non-stream and stream translation into OpenAI `usage.cached_tokens`; the `!translate` response path uses a dual-format parser (`ParseResponseUsage`) that reads Claude `cache_read_input_tokens`/`cache_creation_input_tokens` and OpenAI `prompt_tokens_details.cached_tokens`, so cached tokens survive any provider → OpenAI → Claude double translation. (`internal/translator/gemini.go`, `internal/translator/response.go`)
- **Gemini tool-schema `const` re-injection** — `stripUnsupported` now re-runs after `anyOf`/`oneOf` flattening so `const` and vendor `x-*` keys can't leak back into the merged branch. (`internal/translator/schema.go`)
- **Provider 403 is now retryable** — Gemini/antigravity daily-quota errors can arrive as HTTP 403; these now trigger the connection fallback instead of a hard failure. (`internal/providers/providers.go`)
- **CodeBuddy CN stream cleanup** — The stall reader is now closed after the stream, stopping its shutdown watcher + stall timer (no per-request goroutine leak). (`internal/proxy/executor/codebuddy.go`)

### ⚙️ Graceful Shutdown Hardening

- New `internal/shutdown` package: a process-wide signal the first Ctrl+C / SIGTERM fires.
- `StallReader` now closes in-flight SSE upstream bodies on shutdown, so `server.Shutdown` drains streams in milliseconds instead of waiting out the 15s deadline — and the deferred DB/log-file close always runs.
- Translate-path SSE handlers emit a final `data: [DONE]` on abort so clients get a clean end instead of a truncated stream.
- A second Ctrl+C / SIGTERM force-quits immediately (stuck-drain escape hatch).
- Shutdown timeout logs a warning instead of `log.Fatalf`, so `conn.Close()` and the log file are still closed gracefully. (`internal/proxy/stall.go`, `cmd/9router-go/main.go`, `internal/handlers/chat/forward.go`, `internal/proxy/executor/openai.go`)

### 🔧 Internal

- Stream handlers now carry the request `ctx` and pull accumulated usage (incl. cached tokens) out of the translation session, so logged usage reflects real token counts instead of the character-estimate fallback.

## [v1.7.0] — 2026-08-06

### 🚀 New Executors

- **Trae SOLO remote agent** (`internal/proxy/executor/trae.go`) — Port of `open-sse/executors/trae.js`: `POST {base}/chat_sessions` creates a session, `GET {base}/chat_sessions/{id}/events` streams `plan_item` / `token_usage` / `done` as SSE. Cumulative `plan_item.thought` rendering (longest-wins per id, delta-only emission), `Cloud-IDE-JWT` auth, and `work`/`auto`/manual model modes. Non-stream requests aggregate into a single `chat.completion`. Round-trip test: `TestForwardTrae_StreamsAccumulatedThought`.
- **Windsurf gRPC-web** (`internal/proxy/executor/windsurf.go`) — Port of `open-sse/executors/windsurf.js`: hand-rolled protobuf `GetChatMessageRequest` encoder (Metadata.api_key + cascade_id + model_or_alias + repeated messages), gRPC-web framing (0x00 flag + big-endian length), and a `CompletionChunk` decoder (content / done+UsageStats / error) streaming OpenAI SSE. Catalog→wire model alias map ported verbatim; `crypto/rand` session/cascade ids. Non-stream requests aggregate frames into `chat.completion`. Round-trip test: `TestForwardWindsurf_StreamsGRPCWeb`.

### 🎙️ Xiaomi MiMo TTS

- `/v1/audio/speech` for the `xiaomi-mimo` provider now uses the chat-completions contract (port of `open-sse/handlers/ttsProviders/xiaomi-mimo.js`): target text in `role:assistant`, style/language instructions in `role:user`, voice via top-level `audio.voice`, base64 audio from `choices[0].message.audio.data`. (`internal/handlers/media/media.go`)

### ➕ Providers

- **tokenrouter** — Registered as an OpenAI-compatible upstream (`https://api.tokenrouter.com/v1/chat/completions`).

### 🗑️ Removed

- **qwen provider** — Removed from providers, OAuth config, and the alias map (deprecated upstream).

### 📋 Docs

- `TECHNICAL_DEBT.md` — windsurf + trae moved to resolved; zed + devin-cli documented with the safe-stopgap note (devin-cli corrected: ACP over **stdio** subprocess, not HTTP).

## [v1.6.1] — 2026-08-05

### 🐛 Bug Fixes

- **CodeBuddy CN 502** (`internal/proxy/executor/codebuddy.go`) — `codebuddy-cn` / `codebuddy-intl` now use a dedicated executor that forces `stream=true` upstream (CodeBuddy rejects non-stream with HTTP 400 code 11101), injects the CLI/IDE static headers, and re-aggregates OpenAI-chat SSE into a single `chat.completion` for non-stream clients (`sseToOpenAIJSON`, mirroring JS `parseSSEToOpenAIResponse`).
- Provider parity with the reference implementation.

## [v1.6.0] — 2026-08-04

### 🚀 Next.js Engine Feature Ports

- **TTS Voice Listing** (`/audio/voices`) — Full voice-listing with provider support (`edge-tts` default, `elevenlabs`, `gemini`, `local-device`), `?lang` filter, 24h in-process cache, and `byLang`/`languages` grouping matching the dashboard's media-providers page.
- **Proxy-Pools Deploy** (`/proxy-pools/{vercel,deno,cloudflare}-deploy`) — Deploy edge relay functions to Vercel/Deno/Cloudflare with status polling, plus a new `InsertProxyPool` DB method writing byte-compatible `data` JSON.
- **Headroom Management** (`/headroom/*`) — Full headroom-ai lifecycle in Go: binary/Python detection, spawn/stop/restart, compression extras install/uninstall, `/headroom/proxy` reverse proxy with SSRF guard, and dashboard HTML rewrite.
- **CLI-Tools Status** (`/cli-tools/all-statuses`) — Batch detection of 14 CLI tools (Claude, Codex, OpenCode, etc.) installed state + version.
- **Live Console Logs** (`/translator/console-logs`, `/stream`) — In-process ring buffer + SSE streaming of engine log output so the dashboard's "Monitor Console Log" shows Go logs live (25s keepalive, init/line/clear events).

### 🔍 Observability

- **Lightweight Request Tracing** (`/debug/traces`) — In-memory span recording + p50/p95/p99 latency per provider+model with `?n=` cap. Stdlib-only, no OpenTelemetry SDK dependency.

### 🛡️ Security

- **Prompt-Injection Guard** — Heuristic detection (`messages[]`, `input[]`, Claude content blocks) tagging classic injection attempts in logs. Toggle via `--no-injection-guard` / `INJECTION_GUARD_DISABLED` (on by default).
- **`/admin/health/reset` Moved Behind API-Key Auth** — Previously public; now requires a valid API key to prevent unauthenticated health-state resets (open-source hardening).
- **MITM Binds Loopback Only** — TLS proxy binds `127.0.0.1:443` instead of all interfaces, preventing LAN clients from using it as an open proxy.

### 🐛 Bug Fixes & Stability

- **SSE Fragment Rejoin** — Fixed `unexpected end of JSON input` on opencode free-tier by buffering/rejoining truncated SSE JSON payloads per session (1 MiB cap).
- **Codex/CommandCode Tool-Call Streams** — Stable per-call tool IDs/indices (using upstream `call_id`), correct `[DONE]` framing, and checked `w.Write` errors.
- **MITM Goroutine Leaks** — `Stop()` drains in-flight connections (WaitGroup + active conn close); request bodies bounded at 10 MiB.
- **Executor/OAuth Registry Mutexes** — Package-level registry maps now guarded by `sync.RWMutex` (race-free on re-registration).
- **Token Saver JSON Number Preservation** — `CompressMessages`/`InjectSystemPrompt` use `json.Number` so numeric fields (temperature, large ints) round-trip unchanged.
- **`interface{}` → `any`** — Lint cleanup across stream/log packages.

## [v1.5.0] — 2026-07-24

### 🚀 Architecture & Observability Enhancements

- **Modular `main.go` Refactoring** — Extracted CLI subcommands (`mitmEnable`, `mitmDisable`, `mitmStatus`, `resolveDataDir`) to `cmd/9router-go/commands.go` and encapsulated server routing setup into `handlers.SetupServerRouter()`.
- **Structured Request Logging Middleware** — Moved `statusWriter` and `RequestLogger` to `internal/middleware/logging.go`. Requests are logged with Correlation ID (`id=req_...`) using structured logger (`slog.Info`, `slog.Warn`, `slog.Error`).
- **Dynamic HTTP Status Log Levels** — Requests with status 5xx are logged at `ERROR` level, 4xx at `WARN` level, and 2xx/3xx at `INFO` level for clean log filtering in production.
- **Upstream Memory Exhaustion Protection** — Added `io.LimitReader` caps (1MB for upstream error bodies, 10MB for non-streaming completion bodies) to protect proxy memory from rogue upstreams.
- **Double WriteHeader Prevention** — Added `written bool` guard to `statusWriter` and `cw.IsCommitted()` checks across combo fallback handlers to eliminate `superfluous response.WriteHeader` warnings.
- **Typed Request ID Context Key** — Shared `log.RequestIDKey` across middleware and logging packages to ensure context lookups match reliably.

## [v1.4.0] — 2026-07-23

### 🛠️ Technical Debt Remediations (All 9 Items Resolved)

- **Context-based Per-Request Usage Capture** — Replaced global `translator.lastUsage` with context-captured isolation (`WithUsageCapture`, `SetUsage`, `GetAndClearUsage`) to eliminate cross-request data races under concurrent traffic. (`internal/translator/usage.go`)
- **Thread-safe Daily Usage Updates** — Protected `upsertDailyUsage()` with `dailyUsageMu` mutex to prevent concurrent SQLite read-modify-write races. (`internal/handlers/chat/usage.go`)
- **Committed Response Writer** — Wrapped `http.ResponseWriter` with `committedResponseWriter` to prevent safe-retry attempts after response headers have already been sent to the client. (`internal/handlers/chat/response_writer.go`)
- **Strict Context Propagation** — Replaced all `http.NewRequest` with `http.NewRequestWithContext` across handlers, proxy execution drivers, and OAuth helpers to prevent orphaned upstream connections.
- **Graceful Shutdown** — Implemented `http.Server` graceful shutdown with signal drain (15-second timeout) on SIGINT/SIGTERM. (`cmd/9router-go/main.go`)
- **SQLite Connection Pool Optimization** — Reduced SQLite `SetMaxOpenConns(4)` for optimal WAL mode performance and zero connection contention. (`internal/db/client.go`)
- **Thread-Safe ProxyPool Cache** — Added `sync.Map` `proxyPoolCache` in `internal/db/proxyPools.go` to preserve round-robin rotation indices across requests. (`internal/db/proxyPools.go`)
- **Unbounded Request Body Guard** — Added `middleware.MaxBody` (10MB limit) to protect all endpoints from OOM attacks. (`internal/middleware/max_body.go`, `cmd/9router-go/main.go`)

### ⚡ Metrics, Latency & Token Accounting Fixes

- **TTFT & Latency Tracking** — Added `StartTime` and `TTFT` tracking across all streaming and non-streaming proxy execution drivers (`openai`, `opencode`, `deepseek`, `claude`, `grok-cli`, `qoder`, etc.). (`internal/proxy/executor/`)
- **Input Token Calculation Fix** — Added `[]byte` type support to `CountValueChars` so fallback prompt token calculation accurately estimates token size instead of defaulting to 1 token. (`internal/handlers/chat/chat.go`)
- **Output Token Calculation Fix** — Connected `ResponseBuf` in `executor.Request` to record stream output tokens when upstream omits token usage objects. (`internal/proxy/executor/openai.go`)
- **Prompt Caching Tokens Support** — Updated `OpenAIUsage` to extract `cached_tokens` (`prompt_tokens_details.cached_tokens`) and `cache_creation_input_tokens`. (`internal/translator/types.go`, `internal/handlers/chat/usage.go`)

### 🧪 End-to-End Integration Test Suite

- **E2E Test Suite** — Added `internal/handlers/chat/e2e_integration_test.go` to test real HTTP streaming SSE, non-streaming JSON responses, TTFT latency, token accounting, and SQLite DB usage logging end-to-end.

### 🌐 Endpoints

- **`/api/hello`** — Registered `/api/hello` route returning `200 OK` for ping probes from Claude Code CLI. (`cmd/9router-go/main.go`)

## [v1.3.0] — 2026-07-22

### 🏥 Next.js-Compatible Health System

- **Connection-based health** — Replaced old `kv`-based `IsProviderHealthy`/`RecordProviderHealth` with `modelLock_*` fields in `providerConnections.data` JSON blob, matching Next.js `markAccountUnavailable` / `clearAccountError` flow. (`internal/db/health.go`, `internal/db/accounts.go`)
- **Per-connection model locks** — `LockConnectionModel` / `UnlockConnectionModel` / `IsConnectionModelLocked` use SQLite `json_set()` on shared `providerConnections.data`. Dashboard can read/write same fields. (`internal/db/accounts.go`)
- **`IsProviderAvailable`** — New `Repo` method checks if ANY connection for a provider has no active `modelLock_<model>`, replacing the old kv-based pre-check. (`internal/db/accounts.go`)
- **`POST /admin/health/reset`** — Resets `modelLock_*` on connections via query params `?provider=X&model=X`. Dashboard can call via headroom proxy. (`cmd/9router-go/main.go`)
- **Eliminated duplication** — Package-level `IsProviderHealthy` / `ResetProviderHealth` now delegate to `NewRepo(database)` instead of duplicating lock JSON parsing logic. (`internal/db/health.go`)

### 🧪 Test Fixes

- **False-pass assertions** — 3 handler tests were checking old kv-based `repo.IsModelLocked()` which always returned `false` vacuously. Changed to `repo.IsConnectionModelLocked(connID, model)` to actually verify connection-level locks. (`internal/handlers/chat_test.go`)

## [v1.2.0] — 2026-07-22

### 🎯 Gemini Tool Calling Fixes

- **thought_signature round-trip** — Gemini response encodes `thought_signature` into tool call `id` via `__ts__` separator; request decoder restores it for valid verification. Works for both streaming and non-streaming. (`internal/translator/gemini.go`)
- **Antigravity (AGY) support** — Custom `GeminiPart.UnmarshalJSON` handles `thoughtSignature` (camelCase) AND `thought_signature` (snake_case) since the internal `v1internal` endpoint returns camelCase. (`internal/translator/gemini.go`)
- **Tool response name fix** — `tool_call_id` with `__ts__` suffix no longer corrupts `functionResponse.name` extraction, preventing Gemini validation errors on turn 2. (`internal/translator/gemini.go`)

### 🎨 Logging

- **ANSI color-coded logs** — `INF` = green, `WRN` = yellow, `ERR` = red, `DBG` = cyan. Auto-detects TTY (disabled when piped). Disable via `NO_COLOR=1`. (`internal/log/log.go`)

### 🔧 Streaming Fixes

- **SSE multi-line** — Gemini stream chunks with multiple SSE lines (`data: ...\ndata: ...`) are now split and translated individually. Error on one line continues to next instead of aborting. (`internal/handlers/gemini_handler.go`)

### 🧹 Cleanup

- `fallback.go`: Removed misleading `WRN tokensaver failed` logs — replaced with idiomatic `if next, did := ...; did` pattern.
- `test_opencode.go`: Removed (stale temporary test file).
- `internal/translator/gemini_test.go`: Added (unit tests for `thought_signature` round-trip).

## [v1.1.0] — 2026-07-21

### 🚀 New Features

- **SSRF protection** — `/v1/web/fetch` now blocks requests to private/internal IPs (RFC 1918, loopback, link-local, cloud metadata). Matches Next.js `assertPublicUrl()`. (`internal/handlerutil/ssrf.go`)
- **Bypass handler** — Detects Claude Code naming, warmup, and count requests. Returns fake responses without calling upstream, preventing wasted combo rotation slots. (`internal/handlers/bypass.go`)
- **Structured logging** — New `internal/log` package with Info/Warn/Error/Debug levels, runtime config via `LOG_LEVEL` env var. All ~100 `log.Printf` calls replaced across 24 files.
- **Per-connection model locks** — Model locks now stored as `modelLock_<model>` in `providerConnections.data` JSON blob. DB-compatible with Next.js dashboard. Connection A and B can have independent lock states.
- **SSE stall detection** — `StallReader` wrapper closes upstream connection after 6 minutes of no data, preventing hung streams. Integrated into all 4 SSE stream paths.
- **Error classification** — Text-based error rules (8 patterns) + status-based rules (5 codes) + exponential backoff (2s–5min). Fully matching Next.js `checkFallbackError()`.
- **Retry-after tracking** — Tracks earliest `retryAfter` across combo models, includes `Retry-After` header in error responses.
- **Request ID tracing** — Every response includes `X-Request-ID` header, access log includes `id=xxx` prefix.
- **Combo strategies aligned with Next.js** — Sticky round-robin, auto-capability-switch (vision/pdf detection).
- **Health/lock check in combo loops** — Skip unhealthy or locked models during fallback iteration.

### 🔧 Refactoring

- **Error response consistency** — `WriteJSONError` now status-code-aware (e.g., 401 → `authentication_error`, 429 → `rate_limit_error`). `auth.go` inline JSON replaced.
- **SSE consolidation** — `proxy.WriteSSEHeaders` shared by all 4 SSE stream functions. `proxy.SSECopy` with optional `onChunk` callback.
- **Shared test fixture** — `internal/dbtest` package provides canonical `CreateTables()` eliminating duplicated schema in 5+ test files.
- **`stringBuilder` → `bytes.Buffer`** — Removed duplicate custom type in favor of standard library.

### 📚 Documentation

- `ARCHITECTURE.md` — 10 Mermaid flow diagrams (request lifecycle, combo, fusion, error classification, etc.)
- `DATABASE.md` — All 11 tables, JSON blob structure, Go vs Next.js differences

### 🐛 Fixes

- `RetryAfter` ceiling calculation corrected from floor to proper ceiling (`time.Second - 1`)
- Stream translation now handles `[DONE]` marker before JSON parsing
- `TranslateResp` field now passed in `tryForwardWithConnection`

## [v1.0.2] — Previous

- Initial release with OpenAI/Claude SSE proxy, combo fallback, token savers, benchmark results.
