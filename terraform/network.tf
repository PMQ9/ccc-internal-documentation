data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  azs = slice(data.aws_availability_zones.available.names, 0, var.az_count)
  # /20 VPC split into /24s: public = 0..az_count-1, private = az_count..2*az_count-1
  public_subnet_cidrs  = [for i in range(var.az_count) : cidrsubnet(var.vpc_cidr, 4, i)]
  private_subnet_cidrs = [for i in range(var.az_count) : cidrsubnet(var.vpc_cidr, 4, i + var.az_count)]
}

resource "aws_vpc" "this" {
  cidr_block           = var.vpc_cidr
  enable_dns_support   = true
  enable_dns_hostnames = true
  tags                 = { Name = "${var.name_prefix}-vpc" }
}

resource "aws_internet_gateway" "this" {
  vpc_id = aws_vpc.this.id
  tags   = { Name = "${var.name_prefix}-igw" }
}

resource "aws_subnet" "public" {
  count                   = var.az_count
  vpc_id                  = aws_vpc.this.id
  cidr_block              = local.public_subnet_cidrs[count.index]
  availability_zone       = local.azs[count.index]
  map_public_ip_on_launch = false # ALB gets its own public IPs; nothing else here is public
  tags                    = { Name = "${var.name_prefix}-public-${local.azs[count.index]}", Tier = "public" }
}

resource "aws_subnet" "private" {
  count             = var.az_count
  vpc_id            = aws_vpc.this.id
  cidr_block        = local.private_subnet_cidrs[count.index]
  availability_zone = local.azs[count.index]
  tags              = { Name = "${var.name_prefix}-private-${local.azs[count.index]}", Tier = "private" }
}

# Single NAT gateway (Balanced decision: trims cost vs one-per-AZ; egress SPOF in its AZ accepted).
resource "aws_eip" "nat" {
  domain = "vpc"
  tags   = { Name = "${var.name_prefix}-nat-eip" }
}

resource "aws_nat_gateway" "this" {
  allocation_id = aws_eip.nat.id
  subnet_id     = aws_subnet.public[0].id
  tags          = { Name = "${var.name_prefix}-nat" }
  depends_on    = [aws_internet_gateway.this]
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.this.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.this.id
  }
  tags = { Name = "${var.name_prefix}-public-rt" }
}

resource "aws_route_table_association" "public" {
  count          = var.az_count
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

resource "aws_route_table" "private" {
  vpc_id = aws_vpc.this.id
  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.this.id
  }
  tags = { Name = "${var.name_prefix}-private-rt" }
}

resource "aws_route_table_association" "private" {
  count          = var.az_count
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private.id
}

# Free S3 gateway endpoint so ECR layer pulls / yum keep egress off the NAT (Balanced: trim endpoints).
resource "aws_vpc_endpoint" "s3" {
  vpc_id            = aws_vpc.this.id
  service_name      = "com.amazonaws.${var.region}.s3"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = [aws_route_table.private.id]
  tags              = { Name = "${var.name_prefix}-s3-endpoint" }
}

############################
# VPN allowlist as a managed prefix list (VUIT-supplied; the access control hinges on this)
############################
resource "aws_ec2_managed_prefix_list" "vpn" {
  name           = "${var.name_prefix}-vpn-allow"
  address_family = "IPv4"
  max_entries    = 50

  dynamic "entry" {
    for_each = var.vpn_ingress_cidrs
    content {
      cidr        = entry.value
      description = "Vanderbilt VPN egress (VUIT-supplied)"
    }
  }

  tags = { Name = "${var.name_prefix}-vpn-allow" }
}

############################
# Security groups (no 0.0.0.0/0 ingress anywhere)
############################
resource "aws_security_group" "alb" {
  name        = "${var.name_prefix}-alb"
  description = "ALB: 443 from Vanderbilt VPN CIDRs only"
  vpc_id      = aws_vpc.this.id
  tags        = { Name = "${var.name_prefix}-alb" }
}

resource "aws_vpc_security_group_ingress_rule" "alb_https" {
  security_group_id = aws_security_group.alb.id
  description       = "HTTPS from Vanderbilt VPN prefix list"
  from_port         = 443
  to_port           = 443
  ip_protocol       = "tcp"
  prefix_list_id    = aws_ec2_managed_prefix_list.vpn.id
}

# Port 80 is allowed only to perform the HTTP->HTTPS redirect, still gated to VPN CIDRs.
resource "aws_vpc_security_group_ingress_rule" "alb_http_redirect" {
  security_group_id = aws_security_group.alb.id
  description       = "HTTP from VPN prefix list (redirected to HTTPS)"
  from_port         = 80
  to_port           = 80
  ip_protocol       = "tcp"
  prefix_list_id    = aws_ec2_managed_prefix_list.vpn.id
}

resource "aws_vpc_security_group_egress_rule" "alb_to_app" {
  security_group_id            = aws_security_group.alb.id
  description                  = "To app on 80"
  from_port                    = 80
  to_port                      = 80
  ip_protocol                  = "tcp"
  referenced_security_group_id = aws_security_group.app.id
}

resource "aws_security_group" "app" {
  name        = "${var.name_prefix}-app"
  description = "App EC2: ingress from ALB only; egress to DB/EFS + NAT for image pulls/secrets"
  vpc_id      = aws_vpc.this.id
  tags        = { Name = "${var.name_prefix}-app" }
}

resource "aws_vpc_security_group_ingress_rule" "app_from_alb" {
  security_group_id            = aws_security_group.app.id
  description                  = "HTTP from ALB"
  from_port                    = 80
  to_port                      = 80
  ip_protocol                  = "tcp"
  referenced_security_group_id = aws_security_group.alb.id
}

resource "aws_vpc_security_group_egress_rule" "app_https_out" {
  security_group_id = aws_security_group.app.id
  description       = "HTTPS egress (image pulls, Secrets Manager/SSM, IdP metadata if used)"
  from_port         = 443
  to_port           = 443
  ip_protocol       = "tcp"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "app_to_db" {
  security_group_id            = aws_security_group.app.id
  description                  = "MySQL to RDS"
  from_port                    = 3306
  to_port                      = 3306
  ip_protocol                  = "tcp"
  referenced_security_group_id = aws_security_group.db.id
}

resource "aws_vpc_security_group_egress_rule" "app_to_efs" {
  security_group_id            = aws_security_group.app.id
  description                  = "NFS to EFS"
  from_port                    = 2049
  to_port                      = 2049
  ip_protocol                  = "tcp"
  referenced_security_group_id = aws_security_group.efs.id
}

resource "aws_security_group" "db" {
  name        = "${var.name_prefix}-db"
  description = "RDS: 3306 from app only"
  vpc_id      = aws_vpc.this.id
  tags        = { Name = "${var.name_prefix}-db" }
}

resource "aws_vpc_security_group_ingress_rule" "db_from_app" {
  security_group_id            = aws_security_group.db.id
  description                  = "MySQL from app"
  from_port                    = 3306
  to_port                      = 3306
  ip_protocol                  = "tcp"
  referenced_security_group_id = aws_security_group.app.id
}

resource "aws_security_group" "efs" {
  name        = "${var.name_prefix}-efs"
  description = "EFS: 2049 from app only"
  vpc_id      = aws_vpc.this.id
  tags        = { Name = "${var.name_prefix}-efs" }
}

resource "aws_vpc_security_group_ingress_rule" "efs_from_app" {
  security_group_id            = aws_security_group.efs.id
  description                  = "NFS from app"
  from_port                    = 2049
  to_port                      = 2049
  ip_protocol                  = "tcp"
  referenced_security_group_id = aws_security_group.app.id
}
