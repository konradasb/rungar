---
title: AWS
weight: 4
description: "Runners as EC2 instances."
icon: cloud
sidebar:
  open: false
aliases:
  - /docs/reference/providers/aws/
---

Runs each runner on a fresh EC2 instance, in the first of your subnets with
room for it.

## Requirements

- An AMI with the Actions runner in `/home/runner`, owned by a `runner` user,
  and cloud-init; see [Building an image]({{< relref "building-an-image" >}}).
- An identity for Rungar with `ec2:RunInstances`, `ec2:CreateTags`,
  `ec2:DescribeInstances`, `ec2:DescribeImages` and
  `ec2:TerminateInstances`, and `iam:PassRole` if `instance_profile`, or a
  `launch_template`'s instance profile, is set. On EKS, it can be an IAM
  role for the Helm chart's service account: see
  [On EKS](../../getting-started/kubernetes#on-eks).

## Configuration

```yaml
providers:
  - name: aws
    type: aws
    region: eu-west-1
    subnets: [subnet-0a1b2c3d4e5f60718, subnet-0f1e2d3c4b5a69788]
    runner:
      image: ami-0a1b2c3d4e5f60718
      instance_type: m7i.xlarge
```

Every key is under [Configuration]({{< relref "configuration" >}}).

## Notes

- A job can use its instance's profile. Give runners a role of their own.
- Anyone with `ec2:DescribeInstanceAttribute` can read a registration before
  it is used. Keep runners in an account of their own.
- An encrypted AMI or volume needs grants on its KMS key.
