# Provisioning with Ansible

[`deploy/ansible`](../../deploy/ansible/site.yml) provisions a whole
installation from an inventory: the main server, the judging workers and,
optionally, the contests kept in Git. A run with the same files installs
the same thing, so the repository that holds the inventory is the record
of what runs where.

The playbook runs the release installer (`scripts/install.sh`, see the
[deployment guide](deployment.md)) of a pinned version on each host. The
installer verifies the release checksums, checks the machine and is
idempotent; the playbook adds the order (main server first, then the
workers, a quarter at a time), the secrets the workers need, upgrades and
the contest configuration.

## Use

On the control machine (Ansible 2.14 or newer), with SSH access as a
user that can `sudo` on every host:

```sh
cp -r deploy/ansible ~/cms-infra && cd ~/cms-infra
cp inventory.example.ini inventory.ini    # edit: hosts and the main server's private address
$EDITOR group_vars/all.yml                # version, domain, languages...
ansible-playbook site.yml
```

`inventory.ini`:

```ini
[cms_main]
cms-main.example.org cms_private_ip=10.8.0.1

[cms_workers]
judge-01 ansible_host=10.8.0.11
judge-02 ansible_host=10.8.0.12
```

`cms_private_ip` is the main server's address on the network it shares
with the workers (Valkey and the blob server listen there; see
[external workers](external-worker.md)).

The settings of `group_vars/all.yml`:

| Variable | Meaning |
| --- | --- |
| `cms_version` | the release, `X.Y.Z` (never `latest`: a rerun must install the same thing) |
| `cms_domain`, `cms_email` | HTTPS with Let's Encrypt; empty: plain HTTP on the LAN |
| `cms_admin_allow` | only this network (CIDR) reaches the admin site |
| `cms_languages` | `minimal` or `full` toolchains |
| `cms_tune_host`, `cms_worker_tune_host` | performance governor, no turbo, no transparent huge pages |
| `cms_worker_batch` | workers reinstalled at a time (default 25%) |
| `cms_install_extra_args`, `cms_worker_extra_args` | more installer options, as lists |
| `cms_contest_configs` | [contest directories](contest-config.md) to apply |
| `cms_contest_activate` | apply changed tasks' new datasets live even though the contest has started |

## The administrator's password

The first installation prints the administrator's password once. The
playbook hides the installer's output (`no_log`) so that it never ends up
in Ansible's output or logs; set the password afterwards on the main
server:

```sh
sudo -u cms cmsctl admin-password
```

The secrets the workers need (the Valkey password and the blob token) are
read from the main server's `/etc/cms/secrets.env` and passed to the
workers' installer in tasks that are not logged either.

## Upgrading

Change `cms_version` and run the playbook again. The main server goes
through `cmsctl upgrade` (a backup first, migrations, automatic rollback
if anything fails); workers keep no data and are reinstalled a batch at a
time, so judging never stops. Do not upgrade during a contest.

## What the playbook does not do

- A database replica, continuous archiving and failover are set up by hand
  (see [operations](operations.md)): they depend on your machines and are
  done once.
- The installer's tasks always report "changed": the installer does not
  say whether it changed anything, although a rerun changes nothing.
