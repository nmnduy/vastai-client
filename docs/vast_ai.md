# Setting up vast.ai node

## how to download

```
wget https://raw.githubusercontent.com/vast-ai/vast-python/master/vast.py -O vast; chmod +x vast;
```

## Commands to know to manage vast ai nodes

```
vastai --help

vastai set api-key $(echo -n $VAST_AI_API_KEY)

vastai search offers --help

vastai search offers 'reliability > 0.99  num_gpus>=4' -o 'num_gpus-'

vastai create instance 36842 --image pytorch/pytorch --disk 32

vastai execute 36842 'ls -l'
vastai execute 36842 'rm filename.txt'
```
