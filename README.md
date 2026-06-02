# CCC Internal Documentation
 
Internal knowledge base and documentation platform for the Vanderbilt University College of Connected Computing (CCC).

## Overview
 
A self-hosted wiki for CCC staff to read, write, and maintain internal documentation — covering tools, processes, workflows, and institutional knowledge. Access is restricted to Vanderbilt VPN. Reading requires no login; editing requires Vanderbilt SSO authentication.

 ## Tech Stack

Subject to change
 
| Layer | Technology |
|---|---|
| Platform | [BookStack](https://www.bookstackapp.com/) |
| Hosting | AWS EC2 (t3.small) |
| Database | AWS RDS MySQL (db.t3.micro) |
| File Storage | AWS S3 |
| Auth | Vanderbilt SSO via SAML2/Shibboleth |
| TLS | Let's Encrypt |

## Access Model
 
- **Read** — VPN only, no login required
- **Edit** — VPN + Vanderbilt SSO login
- **Admin** — VPN + Vanderbilt SSO login (elevated role)

Network access is enforced at the AWS Security Group level — the site is unreachable off-VPN.

## Features
 
- Visual (no-code) editor for all staff
- Full page revision history with diffs and one-click restore
- Role-based permissions (Viewer / Editor / Admin)
- Organized as Shelves → Books → Chapters → Pages
## Infrastructure Notes
 
- All content and RBAC metadata stored in RDS MySQL
- Attachments and images offloaded to S3
- SAML2 service provider registration required with VUIT
- DNS subdomain managed by Vanderbilt IT
 
