import json,sys,numpy as np,scipy.sparse as sp,struct,gzip
from sklearn.linear_model import LogisticRegression
from sklearn.metrics import roc_auc_score
D=2**18
extra=sys.argv[1] if len(sys.argv)>1 else None
rows=[]
def load(path):
    ids,labels,groups,dense,si,sv,ip=[],[],[],[],[],[],[0]
    for line in open(path):
        r=json.loads(line)
        ids.append(r['id']);labels.append(r['label']);groups.append(r['group']);dense.append(r['dense'])
        si.extend(r['si'] or []);sv.extend(r['sv'] or []);ip.append(len(si))
    S=sp.csr_matrix((np.array(sv,dtype=np.float32),np.array(si),np.array(ip)),shape=(len(ids),D))
    return ids,np.array(labels),np.array(groups),np.array(dense,dtype=np.float64),S
ids,y,g,X,S=load('feats.jsonl')
if extra:
    i2,y2,g2,X2,S2=load(extra)
    ids+=i2;y=np.concatenate([y,y2]);g=np.concatenate([g,g2]);X=np.vstack([X,X2]);S=sp.vstack([S,S2]).tocsr()
tr=np.array(['|train' in x for x in g])
mu=X[tr].mean(0);sd=X[tr].std(0);sd[sd==0]=1
Z=sp.hstack([sp.csr_matrix((X-mu)/sd),S]).tocsr()
# balance classes
w=np.where(y==1,(y[tr]==0).sum()/(y[tr]==1).sum(),1.0)
MW=float(sys.argv[3]) if len(sys.argv)>3 else 1.0
w=w*np.where(np.char.startswith(g.astype(str),'modern'),MW,1.0)
C=float(sys.argv[2]) if len(sys.argv)>2 else 4.0
clf=LogisticRegression(C=C,max_iter=3000,solver='liblinear')
clf.fit(Z[tr],y[tr],sample_weight=w[tr])
p=clf.predict_proba(Z)[:,1]
te=~tr
print('window AUC test',roc_auc_score(y[te],p[te]))
# threshold: 1% false positives on held-out human windows
hum=te&(y==0)
for q in [0.95,0.98,0.99,0.995]:
    print('human window quantile',q,np.quantile(p[hum],q))
thr=float(sys.argv[4]) if len(sys.argv)>4 else float(np.quantile(p[hum],0.99))
for grp in sorted(set(g[te])):
    m=g==grp
    print(f'{grp:32s} n={m.sum():6d} mean={p[m].mean():.3f} over_thr={(p[m]>=thr).mean():.3f}')
wd=clf.coef_[0][:X.shape[1]];ws=clf.coef_[0][X.shape[1]:]
buf=struct.pack('<II',X.shape[1],D)+np.array(mu,dtype='<f4').tobytes()+np.array(sd,dtype='<f4').tobytes()+np.array(wd,dtype='<f4').tobytes()+np.array(ws,dtype='<f4').tobytes()+np.array([clf.intercept_[0],thr],dtype='<f4').tobytes()
gzip.open('/home/claude/origincheck/assets/aimodel.bin.gz','wb').write(buf)
names=open('/home/claude/origincheck/aifeatures.go').read().split('denseNames = []string{')[1].split('}')[0]
names=[x.strip().strip('"') for x in names.replace('\n','').split(',') if x.strip()]
for n,wv in sorted(zip(names,wd),key=lambda t:-abs(t[1]))[:15]: print(f'{n:18s} {wv:+.3f}')
print('threshold',thr)
