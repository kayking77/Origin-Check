# Training the AI-writing detector

1. Human and AI text: the Ghostbuster dataset (github.com/vivek3141/ghostbuster-data) plus essays written by current Claude models.
2. Put documents in JSON lines `{"id","label"(0 human/1 AI),"group":"name|train or name|test","text"}`.
3. `origincheck featurize < docs.jsonl > feats.jsonl` computes the exact features the app uses.
4. `python3 train.py extra_feats.jsonl 4 10 0.82` trains the logistic regression (C=4, weight 10 on the extra modern samples, sentence threshold 0.82) and writes `assets/aimodel.bin.gz`.
5. `origincheck evaluate < test_docs.jsonl` scores held-out documents with the full report logic. `final_eval.txt` is the result for the shipped model.
