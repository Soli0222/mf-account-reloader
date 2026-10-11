# mf-account-reloader Chart

Kubernetes 1.27以降で、毎日11:00 JSTにone-shot CLIを実行します。
導入・初期認証・再投入のコマンドは[プロジェクトREADME](../../README.md)を参照してください。

```sh
helm lint --strict charts/mf-account-reloader
helm template mfr charts/mf-account-reloader
```

`image.tag`が空なら`.Chart.AppVersion`を使います。Chart versionはアプリversionと独立して管理します。
既定は1GiのPVC、非root UID/GID 10001、fsGroup 10001、読み取り専用root filesystemです。
`auth.stateSecret.name`で既存Secretをread-onlyのseedとしてmountします。
`auth.existingSecret`と`auth.keys`で自動認証用の秘密ファイルを個別に指定できます。
既存PVCは`persistence.existingClaim`、emptyDirは`persistence.enabled=false`で設定します。
Secret本文をvaluesへ書かないでください。

schemaは未知のキーと不正な型を拒否します。Secretが未指定でもoffline lint/renderは成功します。
