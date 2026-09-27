# Problem packages in other formats

The importer also accepts packages made for other systems and converts
them on the fly (the import preview says so):

- `italy-suma/`: the CMS **italy_yaml** format (`task.yaml`, `input/`,
  `output/`, `gen/GEN` subtasks, `cor/correttore` checker, `sol/`,
  `statement/`, `att/`; sample `input0.txt`/`output0.txt` pairs in `att/`
  become the statement's examples).
- `polygon-suma/`: a **Polygon** package (`problem.xml`, generated
  `tests/`, testlib checker, tagged solutions, statements; the LaTeX
  `statement-sections/` become a statement rendered and typeset by the
  CMS, with `example.NN` as its examples).

Zip a folder and import it like any package.
