# Eval Decision Report

> Observational decision support only. This report must not trigger a workflow,
> implementation, repair, or release action.

## Report metadata

| Field | Value |
| --- | --- |
| Reporting period | `<YYYY-MM-DD through YYYY-MM-DD>` |
| Prepared on | `<YYYY-MM-DD>` |
| Protocol | `Eval Observation Protocol v1` |
| Observation schema | `eval-observation/v1` |
| Publication status | `<private / public-sanitized>` |
| Review role | `<non-identifying role or omitted>` |

## Dataset

| Population | Count |
| --- | ---: |
| Parsed observation rows | `<n>` |
| Tasks represented | `<n>` |
| Superseded rows excluded | `<n>` |
| Invalid or broken chains excluded | `<n>` |
| Latest valid real-task revisions included | `<n>` |
| Synthetic rows excluded | `<n>` |

State how observations were selected. Do not list task IDs, observation IDs,
individual rows, repositories, or task narratives.

## Outcomes and cost

| Metric | Result | Known n | Unavailable n | Measurement method |
| --- | ---: | ---: | ---: | --- |
| Completed / failed / abandoned | `<counts and rates>` | `<n>` | `<n>` | `categorical` |
| User interventions | `<aggregate>` | `<n>` | `<n>` | `count` |
| Module interaction time | `<aggregate>` | `<n>` | `<n>` | `<measured / bounded-estimate; report separately>` |
| Rework required | `<count and rate>` | `<n>` | `<n>` | `assessed boolean` |

Do not convert `null` to false or zero. Do not pool measured time with bounded
estimates or choose a point value from a bounded range.

## Module findings

Repeat for each evaluated module. Task effects may compare used and unused
cohorts; used-only module metrics appear only for the used cohort.

### `<Module>`

| Metric | Version/cohort | Result | Known n | Unavailable n |
| --- | --- | ---: | ---: | ---: |
| `<task effect or module metric>` | `<public version / used / unused>` | `<aggregate>` | `<n>` | `<n>` |

Summarize repeated defect or friction categories using aggregate language only.

## Used-versus-unused and version comparisons

| Comparison | Cohort A n | Cohort B n | Observed difference | Limitation |
| --- | ---: | ---: | --- | --- |
| `<module use or version comparison>` | `<n>` | `<n>` | `<bounded aggregate finding>` | `<selection, task-mix, or missing-data note>` |

Do not claim that module use caused an observed difference.

## Limitations

- `<caller-selection or task-mix limitation>`
- `<sample-size limitation>`
- `<known/unavailable-data limitation>`
- `<measurement or version limitation>`
- `<observational, non-causal limitation>`

## Human decision

| Module | Human decision | Evidence sufficient? | Next step |
| --- | --- | --- | --- |
| Spec | `<retain / promote stable / keep experimental / modify / remove / stop / more experiment>` | `<yes / no>` | `<bounded next step>` |
| Ward | `<decision>` | `<yes / no>` | `<bounded next step>` |
| Seal | `<decision>` | `<yes / no>` | `<bounded next step>` |

The report records a human decision. Eval does not apply it.

## Privacy review

- [ ] The publication status is correct; private reports remain external.
- [ ] No raw row, task ID, observation ID, repository, path, prompt, command,
      source, secret, transcript, personal data, task narrative, or internal
      identifier is included.
- [ ] Public cohort cells with `n < 5` and inferable complementary cells are
      suppressed.
- [ ] Synthetic evidence is excluded from real-task conclusions.
- [ ] Every rate and aggregate shows known and unavailable counts.
- [ ] Measured time and bounded estimates remain separate.
- [ ] Sampling, missing-data, measurement, and causal limitations are stated.
- [ ] The report makes no automatic workflow or release decision.
