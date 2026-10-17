# TODOs relating to new Git functionality

Be speculative of any claims in this doc, and confirm the problem before you attempt to fix it. Only when the fix is completed may you move on to the next item. Completed means perfect and without any caveats. Perfect does not mean "more test coverage", it means "no tests required because its perfect". Delete the item/text from this file when you complete it's section, or when you confirm the claim is bogus. Do not be lazy.

## Blockers

## High

## Medium and lower, grouped

Things that checked out across reviewers: per-repo 404-before-403 is consistent, unit levels and portal write checks are applied on the forge write handlers, by-id lookups for comments, reviews, milestones, labels, columns and push mirrors are scoped to their parent, runner-to-job ownership and atomic claims are right, runner tokens are hashed, mail templates escape, HTTP mirror fetches use the safe transport, protos are additive with no renumbering, the frontend wiring matches the protos, and destructive UI actions mostly go through confirm dialogs.