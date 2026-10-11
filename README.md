# mf-account-reloader

Money Forward MEの口座一覧で「金融機関からのデータ一括更新」を押し、必要に応じて指定口座も個別更新するGo製CLIです。処理後に終了し、定期実行はKubernetes CronJobが担当します。残高や明細は収集しません。

**成功は更新要求の受付を意味します。** 金融機関側の取得完了まで確認する場合は`--wait`を指定してください。受付を確認できなかった要求は、自動再送せず終了コード4で終了します。

初めて使う方は「インストール」「認証」「口座の指定」の順に進めてください。DockerとHelmの手順は、その後の実行環境に合わせて参照できます。Go、Chrome/Chromium、Kubernetesの基本操作を前提にしています。

## インストールする

GoとChromeまたはChromiumをインストールしてください。必要なGoのバージョンは[go.mod](go.mod)に記載しています。

```sh
go install github.com/Soli0222/mf-account-reloader/cmd/mf-account-reloader@v0.1.0
export PATH="$(go env GOPATH)/bin:$PATH"
mf-account-reloader version
mf-account-reloader --help
```

Chromeを自動検出できない環境では、`--browser-path`または`MFR_BROWSER_PATH`を指定します。macOSの例：

```sh
export MFR_BROWSER_PATH='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
```

## 専用ブラウザで認証stateを作る

ブラウザで再利用するcookieとlocalStorageを、本アプリ独自のversion付きJSONへ保存します。このファイルを認証stateと呼びます。Playwrightのstorage stateとは互換ではありません。

```sh
mf-account-reloader auth login
mf-account-reloader auth check
```

`auth login`で開く専用ブラウザで、ログイン、メールOTP、追加認証などを完了し、`https://moneyforward.com/accounts`を開いてください。口座画面を確認するとstateを`./data/auth-state.json`へ保存して終了します。更新ボタンは押しません。普段使いのブラウザプロファイルには接続しません。

`run`、`accounts list`、`auth check`は画面を表示しないheadless実行が既定です。headlessでは、起動したChrome/Chromiumのバージョン・OSを使い、User-Agentのブラウザ名を通常表示時と一致させます。`auth login`はheadless設定を明示していなければ画面を表示します。[config.example.yaml](config.example.yaml)の`browser.headless: true`を使う場合は、ログイン時に`--headless=false`を指定してください。

```sh
curl -fsSLo config.yaml \
  https://raw.githubusercontent.com/Soli0222/mf-account-reloader/v0.1.0/config.example.yaml
mf-account-reloader auth login --config config.yaml --headless=false
mf-account-reloader auth check --config config.yaml
```

stateファイルは0600、アプリが作成する専用ディレクトリは0700で保存します。Kubernetesの共有volumeルートでは既存の所有者・fsGroupによる書き込み権限を使い、ファイル自体を0600にします。同じstateを使う処理はOSのファイルロックで排他され、競合すると終了コード6になります。異常終了後もOSがロックを解放するため、`.lock`ファイルを手動削除する必要はありません。

## 口座を一覧し、dry-runで指定を確認する

まず口座のIDと登録名を取得します。このコマンドだけが口座名・IDを明示的に出力します。

```sh
mf-account-reloader accounts list --config config.yaml --format json
mf-account-reloader accounts list --config config.yaml --format text
```

IDは一覧で取得した文字列をそのまま使います。登録名の指定は前後の空白を除いた完全一致です。同名の口座が複数あれば、IDで指定してください。手動管理口座、更新できない口座、存在しない指定は、一括更新を押す前に拒否します。

```sh
mf-account-reloader run --config config.yaml \
  --extra-account-id 'dummy-account-id' --dry-run

# dry-runの結果を確認した後、同じ対象を更新する
mf-account-reloader run --config config.yaml \
  --extra-account-id 'dummy-account-id'

# 追加指定なしなら一括更新のみ
mf-account-reloader

# 金融機関側の処理終了まで待つ
mf-account-reloader run --config config.yaml --wait
```

追加口座はフラグの繰り返し、または`reload.extraAccountIds`と`reload.extraAccountNames`で設定します。IDと名前で同じ口座を指定しても、追加クリックは最大1回です。一括更新の受付を確認したあと、キャッシュを使わず口座一覧をリロードしてから追加更新を判断します。個別更新は1秒以上の間隔で逐次実行し、リロード後も更新中の口座はクリックしません。指定口座の更新ボタンが有効なら個別更新します。

`--wait`では、自動連携口座のうち更新対象と判定できた口座を確認します。手動口座・停止中・要認証の口座は除外件数として結果に残します。未知の状態や安定したIDがない口座があると、完了対象を保証できないため更新前に終了します。取得失敗や対象消失も成功にはしません。

受付確認には受付メッセージまたは更新中への遷移を使い、ボタンの無効化だけでは成功にしません。個別更新は指定口座の更新中表示を確認します。`--wait`の完了には、各対象口座で更新中を観測した後の正常表示が必要です。更新が速すぎるなどの理由で更新中を観測できない場合も、以前の正常表示を完了と扱わず、期限まで確認できなければ終了コード5になります。

一括更新を押した画面では、自動更新対象外の口座も一時的に更新中と表示されます。この表示は完了判定の証拠に使いません。一括更新の受付結果はリロード前に保持します。リロード後に正常と表示されていても、その表示だけで今回の更新が完了したとは判定しません。

## 設定の優先順位と秘密値の入力

設定の優先順位は、明示したCLIフラグ、環境変数、YAML、既定値です。設定ファイルは自動探索しません。`--config`または`MFR_CONFIG`がなければ既定値を使います。CLIの追加口座指定は、環境変数またはYAMLの指定に合併して重複を除きます。

| 環境変数 | 用途 |
| --- | --- |
| `MFR_CONFIG` | YAMLのパス |
| `MFR_AUTH_STATE_FILE` / `MFR_AUTH_SEED_FILE` | 書き込み先state / 初期stateの読み取り専用入力 |
| `MFR_BROWSER_PATH` | Chrome/Chromiumの実行ファイル |
| `MFR_HEADLESS` / `MFR_NO_SANDBOX` | `true`または`false` |
| `MFR_AUTO_LOGIN` / `MFR_WAIT` | 自動再認証 / 完了待機 |
| `MFR_LOG_LEVEL` / `MFR_LOG_FORMAT` | `debug,info,warn,error` / `json,text` |
| `MFR_EXTRA_ACCOUNT_IDS` / `MFR_EXTRA_ACCOUNT_NAMES` | JSON文字列配列。`[]`でYAMLの配列を消去 |
| `MFR_EMAIL` / `MFR_PASSWORD` / `MFR_TOTP_SECRET` | 自動再認証用の秘密値 |
| 上の秘密値に対応する`_FILE` | 秘密ファイルのパス。末尾の改行だけを除去 |

登録名にカンマがあってもJSON配列で指定できます。

```sh
export MFR_EXTRA_ACCOUNT_NAMES='["ダミー口座, 外貨"]'
```

自動再認証は`auth.autoLogin: true`の場合だけ行います。秘密値はYAMLやCLI引数に書かず、環境変数またはファイルで渡してください。同じ秘密値の直接指定と`_FILE`指定を併用すると設定エラーになります。メール/SMSコード、CAPTCHA、追加同意など対話が必要な分岐は終了コード3で終了します。認証失敗を繰り返さず、1回の実行で各認証段階を最大1回だけ送信します。`auth check`では自動再認証しません。

```sh
export MFR_EMAIL_FILE='/path/to/private/email'
export MFR_PASSWORD_FILE='/path/to/private/password'
export MFR_TOTP_SECRET_FILE='/path/to/private/totp-secret'
mf-account-reloader run --config config.yaml --auto-login
```

通常のログはstderrへ、コマンドの結果はstdoutへ出力します。通常結果の追加口座は0始まりの`index`で識別し、生の口座名・IDは出しません。結果の`bulk`と`extra[].status`は`accepted`、`already_updating`、`completed`、`failed`、`unknown`、`planned`を区別します。HTML全文、スクリーンショット、ネットワーク本文は保存しません。

## Dockerで1回実行する

コンテナにはDebianのChromium、CA証明書、日本語フォント、tzdata、子プロセスを回収するtiniを同梱します。UID/GIDは10001です。コンテナの既定YAMLで`browser.noSandbox: true`を設定し、非root、capabilities削除、読み取り専用root filesystemを併用します。ローカルのブラウザsandboxは有効が既定です。

```sh
# LinuxホストではstateディレクトリをUID/GID 10001で書き込み可能にする
sudo install -d -o 10001 -g 10001 -m 0700 ./container-state

docker run --rm --read-only --user 10001:10001 --cap-drop ALL \
  --security-opt no-new-privileges --shm-size 256m \
  --tmpfs /tmp:rw,uid=10001,gid=10001,mode=1777 \
  --mount "type=bind,src=$PWD/container-state,dst=/state" \
  --mount "type=bind,src=$PWD/data/auth-state.json,dst=/seed/auth-state.json,readonly" \
  -e MFR_AUTH_SEED_FILE=/seed/auth-state.json \
  ghcr.io/soli0222/mf-account-reloader:0.1.0 run --dry-run
```

既存の`/state/auth-state.json`がある場合はseedを再投入しません。継続実行ではseedのmountを外しても動きます。秘密ファイルを使う場合は`/credentials`などへread-onlyでmountし、対応する`MFR_*_FILE`にパスを渡します。GUIログインはホストで行ってください。

イメージは`ghcr.io/soli0222/mf-account-reloader:0.1.0`のように、SemVerタグを指定します。タグに`v`は付きません。

## Helmで毎日11:00 JSTに実行する

Kubernetes 1.27以降を対象に、CronJobの`spec.timeZone: Asia/Tokyo`を使います。既定の`concurrencyPolicy`は`Forbid`、Jobの再試行は0回、全体期限は900秒です。アプリ内の期限は認証・更新・待機を含めて10分です。

まずローカルの認証stateをSecretへ投入します。以下は、`mfr` namespace、release名`mfr`を使う例です。

```sh
kubectl create namespace mfr
kubectl -n mfr create secret generic mfr-auth-state \
  --from-file=auth-state.json=./data/auth-state.json
cat > deploy-values.yaml <<'YAML'
auth:
  stateSecret:
    name: mfr-auth-state
    key: auth-state.json
config:
  reload:
    extraAccountIds: [dummy-account-id]
YAML

helm upgrade --install mfr oci://ghcr.io/soli0222/charts/mf-account-reloader \
  --version 0.1.0 -n mfr -f deploy-values.yaml
```

`dummy-account-id`は一覧で取得したIDへ置き換えてください。追加指定が不要なら`extraAccountIds`を省略します。`image.tag`が空ならChartの`appVersion`を使います。Chart versionとアプリversionは独立しています。

stateは既定で1GiのPVCへ保存します。初回だけread-onlyのSecretをseedとして読み取り、認証済みstateをPVCへ書き込みます。既存PVCは`persistence.existingClaim`、一時stateは`persistence.enabled=false`で指定します。Secret名を指定しない既定valuesでもlint/renderは通りますが、stateがなく自動認証も設定されていない実行は未認証として終了します。

自動再認証用の既存Secretを使う場合は、用意したキーだけを指定します。stateのみを使う場合、この設定は不要です。

```yaml
auth:
  existingSecret: mfr-credentials
  keys:
    email: email
    password: password
    totpSecret: totp-secret # TOTPを使わない場合は空文字
config:
  auth:
    autoLogin: true
```

秘密値はvaluesやConfigMapへ埋め込みません。上のキーはread-onlyファイルとしてmountされます。ServiceAccountのtoken自動mountは無効で、Kubernetes API用のRBACは作成しません。

手動実行は次のとおりです。CronJob名は既定で`mfr-mf-account-reloader`です。

```sh
kubectl -n mfr create job --from=cronjob/mfr-mf-account-reloader mfr-manual-$(date +%s)
kubectl -n mfr get jobs
```

手動JobはCronJobの`Forbid`による保護対象には含まれません。同じPVCのstateを使う処理が重なった場合は、アプリのロックが後続を拒否します。

## 認証が切れたら、CronJobを停止してPVCのstateを置き換える

Secretを更新しても、既存PVCのstateは上書きされません。初期seedの仕組みは、壊れたstateや期限切れstateを無言で置き換えないためです。

1. CronJobをsuspendし、実行中Jobがなくなるまで待ちます。
2. ホストの`auth login`で新しいstateを作り、Secretを更新します。
3. 一時PodでSecretからPVCへstateを転送し、Podを削除します。
4. CronJobを再開し、手動Jobで認証を確認します。

```sh
kubectl -n mfr patch cronjob mfr-mf-account-reloader --type=merge \
  -p '{"spec":{"suspend":true}}'
kubectl -n mfr get jobs # ACTIVEが0になったことを確認する

mf-account-reloader auth login --config config.yaml --headless=false
kubectl -n mfr create secret generic mfr-auth-state \
  --from-file=auth-state.json=./data/auth-state.json --dry-run=client -o yaml \
  | kubectl apply -f -

# 既定のPVC名を使用。既存PVCを指定している場合はclaimNameを置き換える。
# imageには使用するアプリのバージョンを指定する。
cat <<'YAML' | kubectl -n mfr apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: mfr-state-transfer
spec:
  restartPolicy: Never
  automountServiceAccountToken: false
  securityContext:
    runAsNonRoot: true
    runAsUser: 10001
    runAsGroup: 10001
    fsGroup: 10001
  containers:
    - name: transfer
      image: ghcr.io/soli0222/mf-account-reloader:0.1.0
      command: [/bin/sh, -ec]
      args:
        - |
          umask 077
          install -m 0600 /seed/auth-state.json /state/.auth-state-transfer
          mv /state/.auth-state-transfer /state/auth-state.json
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities:
          drop: [ALL]
      volumeMounts:
        - name: state
          mountPath: /state
        - name: seed
          mountPath: /seed
          readOnly: true
  volumes:
    - name: state
      persistentVolumeClaim:
        claimName: mfr-mf-account-reloader-state
    - name: seed
      secret:
        secretName: mfr-auth-state
        defaultMode: 0440
YAML
kubectl -n mfr wait --for=jsonpath='{.status.phase}'=Succeeded \
  pod/mfr-state-transfer --timeout=120s
kubectl -n mfr delete pod mfr-state-transfer
kubectl -n mfr patch cronjob mfr-mf-account-reloader --type=merge \
  -p '{"spec":{"suspend":false}}'
kubectl -n mfr create job --from=cronjob/mfr-mf-account-reloader mfr-reauth-$(date +%s)
```

転送Podはstateの内容をログへ出しません。転送に失敗した場合はCronJobを停止したままPodの状態・mount・権限を確認してください。PVC側でファイルロックをサポートするファイルシステムが必要です。

## 終了コードで失敗の種類を判別する

| コード | 意味 |
| --- | --- |
| 0 | 受付確認、すでに更新中、dry-run成功。`--wait`では完了確認 |
| 1 | ブラウザ起動、通信、内部処理の失敗 |
| 2 | 設定不正、曖昧な口座指定、壊れたstate |
| 3 | 未認証、期限切れ、対話が必要な認証、HTTP 401/403によるアクセス拒否 |
| 4 | 更新の部分失敗、画面構造不一致、受付不明 |
| 5 | 全体期限または完了待機のtimeout |
| 6 | 同じstateを利用する処理が実行中 |
| 130 / 143 | SIGINT / SIGTERMで中断。cleanup後に終了 |

## Renovateとリリースを運用する

Renovateは3時間ごとにGo、Go module、Dockerベースイメージ、Chromium、Actions、共有workflowのSHAを更新対象として確認します。Chromiumと関連パッケージは同じPRで更新します。アプリ/runtime更新は`fix(deps)`、Actions更新は`chore(deps)`です。Go本体とbuilderは同じグループです。アプリ依存のmajor更新は自動マージしません。

`main`のconventional commitからrelease-pleaseがrelease PRを作ります。毎週日曜07:00 JSTに、`main`向けのrelease-pleaseブランチで、Appが作成し`autorelease: pending`ラベルを持つPRを確認します。集約checkの`CI required`を含むCIが成功した場合だけsquash mergeします。checkがないPRはマージしません。

release SHAからamd64/arm64イメージを公開し、その成功後に最新mainのChartをOCIへ調停します。各アプリreleaseとChartが必ず1対1になるわけではありません。共有publish処理の結果は`published`、`no-op`、`deferred`です。`deferred`はイメージ未公開などで公開を延期した状態です。Chartの公開状態は、リリースpipelineまたは毎日04:17 JSTの定期処理で確認し、必要に応じて公開します。

再実行には`Retry Release Publish`を使い、存在するreleaseタグに対応するversionだけを入力してください。`0.1.0`なら`v0.1.0`のcommitを解決します。任意refは受け付けず、公開済みSemVer manifestの上書きも拒否します。

| Chartの変更 | PRでのversion更新 |
| --- | --- |
| release-pleaseのappVersionだけ | 更新しない。公開処理が必要ならpatchを採番してmainへcommit |
| image tagだけ | patch |
| templates / values / schema / 梱包内容 | minorまたはmajor |
| Chart新規追加 | 初期versionを設定 |
| `.helmignore`で梱包しないREADMEだけ | 不要 |

### GitHubで確認する設定

release-pleaseと週次マージには、Renovateと同じGitHub Appを使います。必要な設定は次のとおりです。

- Secrets：`RENOVATE_CLIENT_ID`、`RENOVATE_PRIVATE_KEY`。他リポジトリのSecretsは自動共有されません。
- GitHub App：本リポジトリへのインストール。Contents、Issues、Pull requests、Workflowsのwrite、Checksのread権限。権限を変更した場合はインストール側の承認も必要です。
- Actions：workflow実行とreusable workflowの利用許可。release PRはApp tokenで作り、通常のPR CIを起動します。
- GHCR：`GITHUB_TOKEN`から本リポジトリのアプリ/Chart packageへwriteできること。既存packageではrepository accessを設定します。packageのvisibilityはPublicに設定します。
- Branch protection / ruleset：`CI required`をrequired checkに設定し、squash mergeを許可。共有Chart publishは`GITHUB_TOKEN`でmainへversionをcommit backするため、この書き込みを許可する設定が必要です。管理者を含む全pushでPRを必須にする規則のままでは失敗します。

Chart用checkはpath filterでrequired checkを欠落させず、CIの集約checkに含めています。mainへのpushではPR専用Guardをskipし、それ以外のcheckを要求します。

## 実口座なしで検証する

ソースからビルドするには`make build`を実行します。実行ファイルは`bin/mf-account-reloader`に生成されます。

```sh
make build
make fmt
make check                    # Chrome/Chromium、Helm、Python + PyYAML、actionlintが必要
make test-container           # Dockerが必要。最終runtimeイメージで模擬サイトを操作
npx --yes --package renovate@44.149.2 renovate-config-validator --strict renovate.json5
```

CIはGoの整形、vet、unit/race test、実Chromeの統合テスト、Chart検証、workflow lint、Renovate validatorに加え、amd64/arm64のnative runnerでruntimeコンテナの模擬サイトテストを行います。CIへ実認証情報は渡しません。

Money Forwardの画面構造に依存するため、サイト側の変更で操作できなくなる場合があります。DOM解析は[internal/moneyforward/dom.js](internal/moneyforward/dom.js)にまとめています。未知のコントロールや複数候補を検出した場合は停止します。認証にIndexedDBが必要な環境は、現在のstate形式では対応していません。

不具合の報告先は[GitHub Issues](https://github.com/Soli0222/mf-account-reloader/issues)です。秘密値、state本文、口座名・ID、画面全体を添付せず、コマンド名と終了コードを記載してください。

用語：stateは保存する認証JSON、seedはstate不在時だけ読む初期入力、PVCはstateを永続化するKubernetes volume、OCI Chartはコンテナレジストリで配布するHelm packageです。

最終更新：2026-10-11。

## ライセンス

[MIT License](LICENSE)。Copyright (c) 2026 Soli。

参照元の著作権表示は[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)に記載しています。
