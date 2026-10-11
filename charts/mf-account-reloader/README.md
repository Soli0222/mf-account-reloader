# mf-account-reloader Chart

Kubernetes 1.27以降で、毎日11:00 JSTにone-shot CLIを実行します。
導入・初期認証・再投入のコマンドは[プロジェクトREADME](../../README.md)を参照してください。

```sh
helm lint --strict charts/mf-account-reloader
helm template mfr charts/mf-account-reloader
```

`image.tag`が空なら`.Chart.AppVersion`を使います。Chart versionはアプリversionと独立して管理します。
リソース名は、リリース名にChart名（`nameOverride`指定時はその値）が含まれていればリリース名をそのまま使い、それ以外は`リリース名-Chart名`にします。
`fullnameOverride`でリソース名を直接指定できます。名前は52文字までに切り詰めます。

0.2.0では名前の重複を解消しました。リリース名が`mf-account-reloader`なら、CronJob名は`mf-account-reloader`になります。
ConfigMap・ServiceAccount・PVCの既定名も変わります。既存の認証状態を引き継ぐ場合は、更新前に`persistence.existingClaim`へ現在のPVC名を指定してください。
従来の名前を維持する場合は、`fullnameOverride`へ従来のリソース名を指定します。

既定は1GiのPVC、非root UID/GID 10001、fsGroup 10001、読み取り専用root filesystemです。
`auth.stateSecret.name`で既存Secretをread-onlyのseedとしてmountします。
`auth.existingSecret`と`auth.keys`で自動認証用の秘密ファイルを個別に指定できます。
既存PVCは`persistence.existingClaim`、emptyDirは`persistence.enabled=false`で設定します。
Secret本文をvaluesへ書かないでください。

schemaは未知のキーと不正な型を拒否します。Secretが未指定でもoffline lint/renderは成功します。
