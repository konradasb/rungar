# TODO: the aws provider

## Before it is relied on

- **A runner that finishes is often recorded as lost.** Instances terminate
  themselves on shutdown, so one that finishes leaves `List` at once. If
  that happens before Rungar handles GitHub's job-completed message, the
  reconciler forgets the runner as `LossGone` ("Runner lost: its VM on aws
  is gone"), not `RemovalStopped`. A runner that crashes or never connects
  is recorded as lost too. Stop on shutdown instead, and let Rungar's Delete
  terminate the stopped instance, as the gcp provider does. The instance
  then costs only its volume until the next pass.
- **Try it on real EC2.** The provider is tested against a fake only. Check
  the capacity and quota error codes, and whether they come back
  synchronously (Spot most of all). Check that the default user data runs on
  stock Ubuntu and Amazon Linux AMIs, and that tag filters on keys such as
  `rungar.sh/scale-set` work.
- **Test it on the wire.** The fake stands in for the `ec2API` interface,
  so the requests the SDK sends are never checked. Add a test against
  LocalStack or moto behind the `e2e` build tag.

## At scale

- **Instances that fail after launch go unseen.** One that goes from
  `pending` to `terminated` (`Server.InsufficientInstanceCapacity`, a Spot
  instance taken back) just vanishes. It is never reported as full, so the
  scale set keeps trying the provider rather than holding off.
- **EC2's request limits.** Every scale set lists every reconcile pass.
  With many scale sets in one account, `RequestLimitExceeded` fails `List`
  and the provider looks unreachable. Share one short-lived listing per
  provider, or use the SDK's adaptive retry mode.
- **User data's size is not checked.** EC2 takes 16 KB before encoding. A
  large `user_data` and the registration together can exceed it, and then
  every runner is refused as an invalid parameter, found only at create.
  Check it when the configuration loads, with room for the registration.
- **The endpoint does not stand for the account.** It is
  `aws:<profile>/<region>`, so two profiles for one account pass the check.
  If their subnets overlap, each provider lists and counts the other's
  instances.

## Features

- **Launch templates**: a `launch_template` key would cover key pairs,
  extra volumes, placement groups and capacity reservations, without a
  Rungar key for each.
- **Fallback instance types**, such as `[m7i.xlarge, m6i.xlarge]`, tried
  before the next subnet. On Spot it finds more capacity than more subnets.
- **Volume settings**: gp3 IOPS and throughput, and a KMS key.
- **AMIs by SSM parameter**, such as `resolve:ssm:/aws/service/...`, to
  follow a distribution's latest image. The root device lookup has to be
  reworked for it: it asks EC2 about an AMI ID.
- **EKS with an IAM role for the service account.** The SDK's default chain
  should find the credential already; the chart does not say how, and its
  values say nothing of AWS.

## Minor

- **Assuming a role directly**: a `role_arn` key. Today it goes through a
  profile.
- **Say who can read the registration.** Anyone with
  `ec2:DescribeInstanceAttribute` in the account can read a runner's user
  data, and with it the registration, until the runner has used it. The
  reference page says only that a job can.
- **Graviton instances** work when the AMI's architecture matches, but
  Rungar assumes one architecture; see "Architecture and OS in the model" in
  the top-level TODO.md.
