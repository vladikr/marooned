# Node mode is not in this repository

Guest-kubelet / VM-is-a-Node isolation, including group mode, lives in
https://github.com/vladikr/maroonedpods

This operator only implements RuntimeClass `marooned` (sandbox). A Pod labeled
`maroonedpods.io/maroon=true` is **not** opted into isolation here.

Do not install this operator and maroonedpods in the same cluster. They share
`MaroonedPodsConfig`.
