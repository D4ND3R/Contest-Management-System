# Contest configuration in Git

A contest's configuration can live in a Git repository and be applied to
an installation deterministically: the same directory always gives the
same contest, and applying it twice changes nothing the second time. Task
setters review changes as diffs, and the contest of the rehearsal is
exactly the contest of the real day.

## The directory

```
ioi2030/
├── contest.yaml
└── tasks/
    ├── sum/          a problem package (see problem-package.md)
    │   ├── problem.yaml
    │   ├── statement/en.md
    │   └── tests/...
    └── tree/
        └── ...
```

`contest.yaml`:

```yaml
format: 1
name: ioi2030
settings:
  title: International Olympiad in Informatics 2030
  start_time: "2030-07-01T09:00:00Z"
  stop_time: "2030-07-01T14:00:00Z"
  languages: [C++17 / g++, Python 3]
  max_submission_number: 50
  score_visibility: full
  # ... any contest setting, by the names `export` writes
tasks: [sum, tree]
```

`settings` takes the contest settings of the admin panel. Those it leaves
out keep their value (a new contest gets the defaults). Some are refused
on purpose:

- `invitation_code`: a secret, it does not belong in a repository;
- `status`, `submissions_paused`, `pause_message`, `ranking_unfrozen`:
  operational state, changed from the admin panel during the contest;
- the time zone: the server's (**Server** page).

Contestants are not part of the configuration: their credentials must not
be in a repository. Import them from CSV in the admin panel.

Each directory under `tasks/` is a problem package in this system's format
(or italy_yaml / Polygon, which are converted); its `name` must be the
directory name. `tasks` gives the order of the tasks in the contest.

## Starting from an existing contest

```sh
sudo -u cms cmsctl contest-config export -contest ioi2030 /srv/ioi2030
cd /srv/ioi2030 && git init && git add . && git commit -m "ioi2030 as configured"
```

`export` writes `contest.yaml` and `tasks/` (the live dataset of each task,
with its statements, attachments, examples and reference solutions) and
leaves the other files of the directory (`.git`, a README) alone. It is
deterministic: exporting twice gives identical files.

## Applying

```sh
sudo -u cms cmsctl contest-config apply -dry-run /srv/ioi2030   # what would change
sudo -u cms cmsctl contest-config apply /srv/ioi2030
```

Every package is read and checked first: one error anywhere and nothing is
applied. Then:

- the contest is created, or the settings that differ are updated (the
  output names them);
- a new task is imported into the contest;
- a task whose package changed gets a **new dataset**, and its title,
  statements, attachments, examples and settings from `problem.yaml`
  follow the package (statements removed from the package are removed).
  Before the contest starts the new dataset becomes the live one. Once it
  has started, the new dataset waits: review it in the admin panel (turn
  on background judging for it, then **Compare with the live dataset**
  shows how every score would change), make it live there, or apply again
  with `-activate`;
- the tasks are numbered in the order of `tasks`. A task of the contest
  that the file does not list is left alone and reported (removing a task
  is done from the admin panel).

Whether a task changed is decided by its content: the dataset created by
`apply` carries the SHA-256 of the package directory in its description
(`Default @3f2a9c1b7d4e`), and a directory written by `export` is
recognised as identical to what is live. Hidden files (`.git`,
`.gitignore`) and editor leftovers are ignored.

## With Ansible

The [Ansible playbook](ansible.md) copies the directories listed in
`cms_contest_configs` to the main server and applies them, so the whole
installation, contest included, comes from one repository.
