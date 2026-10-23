# Initial feedback evaluation — 2026-10-23

## Scope

The final run used 24 synthetic single-turn scenarios: 11 report-worthy and 13
skip cases, including the user's Cargo-target and queue-duration incidents.
GPT-6.1 Sol and GPT-6 Astra each ran every case twice at low reasoning effort.
The API returned the requested model IDs. Four requests ran concurrently; all 192
completed without infrastructure errors. Generated feedback calls were never
executed. Claude Code was tested separately through a scripted integration fixture.

The A/B comparison held the tool name and structured schema constant. **Baseline**
used a short generic feedback description; **clarified** used the production
instructions. This does not compare against Anthropic's native tool or its full
system prompt. Both arms benefit from the new schema's category definitions.

## Results

| Model | Description | Report decisions | Skip decisions | All applicable field checks |
| --- | --- | --- | --- | --- |
| GPT-6.1 Sol | Baseline | 22/22 | 26/26 | 48/48 |
| GPT-6.1 Sol | Clarified | 22/22 | 26/26 | 48/48 |
| GPT-6 Astra | Baseline | 22/22 | 26/26 | 48/48 |
| GPT-6 Astra | Clarified | 22/22 | 26/26 | 48/48 |

These scores use the corrected deterministic grader on the retained model outputs.
Tool name/schema, category/failure mode, recovery status, reproduction status,
known evidence anchors, and literal credential omission are checked separately.
There were no false-positive or false-negative selections in this sample.
The result establishes a passing regression baseline; it **does not establish
that the longer description improves selection**. The cases and shared schema
make the intended distinctions relatively easy, and two repeats per case do not
estimate rare production failures.

Both models classified the supplied Cargo and queue incidents as recovered model
behavior, with untested reproduction, under both descriptions. Manual spot review
covered those examples, user-reported versus observed repeats, destructive recovery,
credential omission, and the disabled-tool response. Some outputs explicitly
qualified synthetic session events as user-supplied, illustrating a limitation of
single-turn narratives rather than actual multi-turn tool histories.

## Grader corrections retained in the audit trail

An initial 16-case pilot (64 requests, one repeat) scored baseline 32/32 and
clarified 30/32. The two failures came from an ambiguous fixture: it described
three failures without establishing who performed the requests. Both clarified
outputs correctly avoided claiming independent reproduction. The final dataset
made observed repeats explicit and added a separate user-reported-repeat case.
No tool-description change was made in response.

The final 192-request run initially scored baseline 96/96 and clarified 95/96.
One correct response said it had **not** independently verified the user's report,
but a forbidden-substring check matched “independently verified” inside the
negation. Removed the context-insensitive prose prohibitions; structured
`reproduction: untested` checks remain, and a grader regression test rejects an
unsupported `verified` value. Replaying the same outputs produced the table above;
no model calls were regenerated to obtain the corrected score. Free-text truth is
still a human-review task, not a guarantee of the deterministic grader.

## Reproducibility

Commands and environment options are in [development](development.md#feedback-tests-and-evals).
The final run completed at `2026-10-23T12:55:32.238946Z`. Input hashes
below correspond to the corrected fixtures/grader labels; the model-facing
scenario text was unchanged by the regrade.

| Input | SHA-256 |
| --- | --- |
| baseline | `3585058ab004d7619170a77effad7627cb9a088b219776a24267949e2e4d7144` |
| cases | `0273506b089137baadcdf5907092bac4db0692824218fb7cad281466af8a011d` |
| description | `e68e22afe9ac433971565915fd8f3ac1e6d4b0b5562afd8be490f3ae95e1062f` |
| schema | `fdffb71f2b390900f46b4ecc3e0e50a92776800eef30ddcee9116a54e1bd7ec0` |
| system | `5b5ba470859254a70ab2510a0df083b27ab2f30f34a155d465a367252290aa0e` |

The local run artifacts retain original outputs and a separate regraded artifact,
including source and correction metadata. They are not production conversation
captures. Future runs also write these fingerprints directly into the artifact.

Next validation should use sanitized real multi-turn traces, independently labeled
by a human, including competing tool use, post-error retries, cached tools after
disable, and reports claiming unsupported causality. Measure selection precision
and recall separately from category accuracy, evidence support, duplicate rate,
and false claims of saving or fixing. Keep held-out traces separate from examples
used to tune the instructions.
