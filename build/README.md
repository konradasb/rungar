# Linux packages

The `rungar` deb and rpm package, and the apt and dnf repository at
`https://pkg.rungar.sh` it is published to.

| Path | |
|---|---|
| `package/` | What the package installs besides the binary: the service, the sysusers setting, and each format's install and removal scripts. [.goreleaser.yml](../.goreleaser.yml) says where each goes. |
| `repo/` | Adds a release's packages to the repository, signs it, and uploads it. |
| `test-packages.sh` | Installs the packages from a test repository on each distribution, in docker. |
| `runner-image/` | Not the package: the Packer templates for the images the gcp and aws providers' runners boot, and the script both run to install the Actions runner. See each provider's Building an image guide: [gcp](../docs/content/docs/providers/gcp/building-an-image.md), [aws](../docs/content/docs/providers/aws/building-an-image.md). |

## Building and testing

```console
make packages       # the packages of every architecture, into dist/, unsigned
make test-packages  # then installs them on each distribution, in docker
```

The [test-packages workflow](../.github/workflows/test-packages.yaml) runs
both for a pull request that changes what makes the packages.

## Publishing

A release builds the packages, and signs the RPMs, as dnf checks each one's
signature. Publishing it runs the
[packages workflow](../.github/workflows/packages.yaml), which verifies them
against the release's signed checksums, adds them to the repository with
`repo/build.sh`, and uploads it with `repo/push.sh`. The repository is an R2
bucket: the packages already there stay, and the metadata is signed again.
A pre-release is not published.

Run the workflow by hand, with a tag, to publish a release's packages again.
It refuses RPMs not signed with the repository's key, as dnf would; a
release built without the key needs building again.

## Setting it up

Once:

1. **A signing key**, with no passphrase, as the key's secret is what
   protects it. RSA, which every version of rpm and apt checks:

   ```console
   gpg --batch --passphrase '' --quick-gen-key 'Rungar Packages <maintainers@rungar.sh>' rsa4096 sign never
   gpg --armor --export-secret-keys 'Rungar Packages' > rungar-packages.key
   gh secret set PACKAGES_GPG_PRIVATE_KEY -R konradasb/rungar < rungar-packages.key
   ```

   The release workflow signs the RPMs with it, and the packages workflow
   the repository. Keep a copy offline, and delete the file: replacing the
   key means every user trusting the new one.

2. **A bucket**, in Cloudflare R2, with `pkg.rungar.sh` connected as its
   custom domain, and an R2 API token that can read and write its objects
   alone: *R2 → Manage API tokens → Create API token*, with *Object Read &
   Write*, for that bucket.

3. **The repository's settings**, under *Secrets and variables → Actions*:

   | Name | Kind | |
   |---|---|---|
   | `PACKAGES_S3_ACCESS_KEY_ID` | secret | The token's access key ID. |
   | `PACKAGES_S3_SECRET_ACCESS_KEY` | secret | Its secret access key. |
   | `PACKAGES_BUCKET` | variable | The bucket's name. |
   | `PACKAGES_S3_ENDPOINT` | variable | Its S3 endpoint, `https://ACCOUNT_ID.r2.cloudflarestorage.com`. |
