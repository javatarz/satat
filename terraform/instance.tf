data "aws_ami" "ubuntu" {
  most_recent = true
  owners      = ["099720109477"]

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-arm64-server-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

data "aws_vpc" "default" {
  default = true
}

data "aws_caller_identity" "current" {}

data "aws_iam_policy_document" "instance_assume_role" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "instance" {
  name               = "satat-instance"
  assume_role_policy = data.aws_iam_policy_document.instance_assume_role.json
}

data "aws_iam_policy_document" "instance" {
  statement {
    sid    = "PublishWireGuardPublicKey"
    effect = "Allow"

    actions = ["ssm:PutParameter"]

    resources = [
      "arn:aws:ssm:${var.region}:${data.aws_caller_identity.current.account_id}:parameter/satat/wireguard/*",
    ]
  }
}

resource "aws_iam_role_policy" "instance" {
  name   = "satat-instance"
  role   = aws_iam_role.instance.id
  policy = data.aws_iam_policy_document.instance.json
}

resource "aws_iam_instance_profile" "instance" {
  name = "satat-instance"
  role = aws_iam_role.instance.name
}

resource "aws_security_group" "satat" {
  name        = "satat"
  description = "Satat ingress: HTTPS and WireGuard only"
  vpc_id      = data.aws_vpc.default.id

  ingress {
    description = "HTTPS"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    description = "WireGuard"
    from_port   = 51820
    to_port     = 51820
    protocol    = "udp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    description = "All outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "satat"
  }
}

resource "aws_instance" "satat" {
  ami                    = data.aws_ami.ubuntu.id
  instance_type          = var.instance_type
  vpc_security_group_ids = [aws_security_group.satat.id]
  iam_instance_profile   = aws_iam_instance_profile.instance.name

  root_block_device {
    volume_type = "gp3"
    volume_size = var.root_volume_size
    encrypted   = true
  }

  user_data = templatefile("${path.module}/cloud-init.yml.tftpl", {
    deploy_wg_public_key  = var.deploy_wg_public_key
    laptop_wg_public_key  = var.laptop_wg_public_key
    deploy_ssh_public_key = var.deploy_ssh_public_key
    owner_ssh_public_key  = var.owner_ssh_public_key
    region                = var.region
  })

  user_data_replace_on_change = true

  instance_market_options {
    market_type = "spot"

    spot_options {
      spot_instance_type             = "persistent"
      instance_interruption_behavior = "stop"
    }
  }

  tags = {
    Name = "satat"
  }
}

resource "aws_eip" "satat" {
  domain   = "vpc"
  instance = aws_instance.satat.id

  tags = {
    Name = "satat"
  }
}

output "instance_ip" {
  description = "Elastic IP of the Satat VM; point the SATAT_DOMAIN A record here."
  value       = aws_eip.satat.public_ip
}
